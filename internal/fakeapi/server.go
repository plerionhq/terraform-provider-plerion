// Package fakeapi is an in-memory stand-in for the Plerion custom checks API,
// used by the provider's tests. It copies the behavior of the real API that
// the provider depends on: full replacement on update, no version bump on a
// no-op update, unique slugs, and metadata returned with its keys reordered
// and its numbers rewritten as 64-bit floats.
package fakeapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// APIKey is the only key the fake accepts.
const APIKey = "test-api-key"

const (
	organizationID = "00000000-0000-4000-8000-00000000000a"
	tenantID       = "00000000-0000-4000-8000-00000000000b"
	actorID        = "00000000-0000-4000-8000-00000000000c"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Target mirrors the API's target object.
type Target struct {
	AssetType string `json:"assetType"`
	Scope     string `json:"scope"`
}

// AdditionalAsset mirrors an entry of the API's additionalAssets array.
type AdditionalAsset struct {
	AssetType string `json:"assetType"`
}

// Defaults mirrors the API's defaults object.
type Defaults struct {
	SeverityLevel string         `json:"severityLevel"`
	Message       string         `json:"message"`
	Informational *bool          `json:"informational"`
	Remediation   *string        `json:"remediation,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
}

// Writable is the request body for create and update.
type Writable struct {
	Slug             string            `json:"slug"`
	Title            string            `json:"title"`
	Description      *string           `json:"description,omitempty"`
	Target           *Target           `json:"target"`
	AdditionalAssets []AdditionalAsset `json:"additionalAssets,omitempty"`
	Defaults         *Defaults         `json:"defaults"`
	Type             string            `json:"type"`
	Body             string            `json:"body"`
}

// Rollup mirrors the run summary the API attaches to reads.
type Rollup struct {
	OverallStatus     string `json:"overallStatus"`
	IntegrationCount  int    `json:"integrationCount"`
	FailedCount       int    `json:"failedCount"`
	CompletedCount    int    `json:"completedCount"`
	TotalFindingCount int    `json:"totalFindingCount"`
}

// Check is a stored custom check.
type Check struct {
	Writable

	ID        string
	Version   int
	CreatedAt string
	UpdatedAt string
	LastRunAt string
	Rollup    *Rollup
}

// DryRunOutcome controls what a dry run reports once it finishes.
type DryRunOutcome struct {
	Status      string
	FailedCount int
	Failures    []map[string]any
	Findings    []map[string]any
	// PollsBeforeDone is how many polls report RUNNING first. Set it very
	// high to simulate a dry run that never finishes.
	PollsBeforeDone int
}

type dryRun struct {
	id      string
	checkID string
	outcome DryRunOutcome
	polls   int
}

// Server is a running fake API.
type Server struct {
	*httptest.Server

	mu             sync.Mutex
	checks         map[string]*Check
	order          []string
	dryRuns        map[string]*dryRun
	dryRunOutcome  DryRunOutcome
	requestCounter map[string]int
	clock          time.Time
}

// New starts a fake API and stops it when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	server := &Server{
		checks:         map[string]*Check{},
		dryRuns:        map[string]*dryRun{},
		requestCounter: map[string]int{},
		clock:          time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		dryRunOutcome: DryRunOutcome{
			Status:          "SUCCEEDED",
			PollsBeforeDone: 1,
			Findings: []map[string]any{
				{"id": "finding-1", "status": "FAILED", "severityLevel": "HIGH", "assetId": "asset-1"},
			},
		},
	}
	server.Server = httptest.NewServer(http.HandlerFunc(server.handle))
	t.Cleanup(server.Close)
	return server
}

// Check returns a copy of the stored check, or nil.
func (s *Server) Check(id string) *Check {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.checks[id]
	if !ok {
		return nil
	}
	copied := *stored
	return &copied
}

// CheckBySlug returns a copy of the stored check with the slug, or nil.
func (s *Server) CheckBySlug(slug string) *Check {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stored := range s.checks {
		if stored.Slug == slug {
			copied := *stored
			return &copied
		}
	}
	return nil
}

// Seed stores a check as if someone created it outside Terraform.
func (s *Server) Seed(input Writable) *Check {
	s.mu.Lock()
	defer s.mu.Unlock()
	check := s.insert(input)
	copied := *check
	return &copied
}

// Mutate changes a stored check as if someone edited it outside Terraform.
// It bumps the version like a real update.
func (s *Server) Mutate(id string, change func(check *Check)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.checks[id]
	if !ok {
		panic("fakeapi: no check with ID " + id)
	}
	change(stored)
	stored.Version++
	stored.UpdatedAt = s.tick()
}

// Remove deletes a stored check as if someone deleted it outside Terraform.
func (s *Server) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.remove(id)
}

// SetDryRunOutcome sets the result of dry runs started from now on.
func (s *Server) SetDryRunOutcome(outcome DryRunOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dryRunOutcome = outcome
}

// Requests reports how many requests matched "METHOD /path/pattern", where
// check and dry-run IDs are replaced by {id}.
func (s *Server) Requests(route string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestCounter[route]
}

func (s *Server) handle(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if request.Header.Get("Authorization") != "Bearer "+APIKey {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"message": "Unauthorized"})
		return
	}

	segments := strings.Split(strings.Trim(request.URL.Path, "/"), "/")
	if len(segments) < 3 || segments[0] != "v1" || segments[1] != "tenant" {
		writeJSON(response, http.StatusNotFound, map[string]any{"message": "Not Found"})
		return
	}

	route := request.Method + " /" + strings.Join(segments[:3], "/")
	if len(segments) == 4 {
		route += "/{id}"
	}
	s.requestCounter[route]++

	switch {
	case segments[2] == "custom-checks" && len(segments) == 3 && request.Method == http.MethodGet:
		s.list(response, request)
	case segments[2] == "custom-checks" && len(segments) == 3 && request.Method == http.MethodPost:
		s.create(response, request)
	case segments[2] == "custom-checks" && len(segments) == 4 && request.Method == http.MethodGet:
		s.get(response, segments[3])
	case segments[2] == "custom-checks" && len(segments) == 4 && request.Method == http.MethodPut:
		s.update(response, request, segments[3])
	case segments[2] == "custom-checks" && len(segments) == 4 && request.Method == http.MethodDelete:
		s.delete(response, segments[3])
	case segments[2] == "custom-check-dry-runs" && len(segments) == 3 && request.Method == http.MethodPost:
		s.startDryRun(response, request)
	case segments[2] == "custom-check-dry-runs" && len(segments) == 4 && request.Method == http.MethodGet:
		s.pollDryRun(response, segments[3])
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (s *Server) list(response http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	limit := 50
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			writeErrors(response, http.StatusBadRequest, fieldError("limit", "must match pattern \"^[1-9][0-9]{0,3}$\""))
			return
		}
		limit = min(parsed, 200)
	}

	start := 0
	if cursor := query.Get("cursor"); cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(cursor)
		if err == nil {
			start, err = strconv.Atoi(string(decoded))
		}
		if err != nil {
			writeErrors(response, http.StatusBadRequest, fieldError("cursor", "is not valid"))
			return
		}
	}

	var matches []*Check
	for _, id := range s.order {
		check := s.checks[id]
		if assetType := query.Get("assetType"); assetType != "" && check.Target.AssetType != assetType {
			continue
		}
		if scope := query.Get("scope"); scope != "" && check.Target.Scope != scope {
			continue
		}
		matches = append(matches, check)
	}

	end := min(start+limit, len(matches))
	items := make([]json.RawMessage, 0, end-start)
	for _, check := range matches[min(start, end):end] {
		items = append(items, render(check, true))
	}

	page := map[string]any{"items": items}
	if end < len(matches) {
		page["nextCursor"] = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(end)))
	}
	writeJSON(response, http.StatusOK, page)
}

func (s *Server) create(response http.ResponseWriter, request *http.Request) {
	input, ok := decodeWritable(response, request)
	if !ok {
		return
	}
	if s.slugTaken(input.Slug, "") {
		writeErrors(response, http.StatusConflict,
			map[string]any{"message": fmt.Sprintf("Custom check with slug %q already exists for this tenant", input.Slug)})
		return
	}
	check := s.insert(input)
	writeRaw(response, http.StatusOK, render(check, false))
}

func (s *Server) get(response http.ResponseWriter, id string) {
	check, ok := s.checks[id]
	if !ok {
		writeErrors(response, http.StatusNotFound, map[string]any{"message": "Custom check not found"})
		return
	}
	writeRaw(response, http.StatusOK, render(check, true))
}

func (s *Server) update(response http.ResponseWriter, request *http.Request, id string) {
	check, ok := s.checks[id]
	if !ok {
		writeErrors(response, http.StatusNotFound, map[string]any{"message": "Custom check not found"})
		return
	}
	input, ok := decodeWritable(response, request)
	if !ok {
		return
	}
	if s.slugTaken(input.Slug, id) {
		writeErrors(response, http.StatusConflict,
			map[string]any{"message": fmt.Sprintf("Custom check with slug %q already exists for this tenant", input.Slug)})
		return
	}

	if !reflect.DeepEqual(normalize(check.Writable), normalize(input)) {
		check.Writable = input
		check.Version++
		check.UpdatedAt = s.tick()
		check.Rollup = nil
	}
	writeRaw(response, http.StatusOK, render(check, false))
}

func (s *Server) delete(response http.ResponseWriter, id string) {
	if _, ok := s.checks[id]; !ok {
		writeErrors(response, http.StatusNotFound, map[string]any{"message": "Custom check not found"})
		return
	}
	s.remove(id)
	writeJSON(response, http.StatusAccepted, map[string]any{"accepted": true})
}

func (s *Server) startDryRun(response http.ResponseWriter, request *http.Request) {
	var input struct {
		IntegrationID string `json:"integrationId"`
		Check         struct {
			CustomCheckID    string            `json:"customCheckId"`
			Slug             *string           `json:"slug"`
			Title            *string           `json:"title"`
			Description      *string           `json:"description"`
			Target           *Target           `json:"target"`
			AdditionalAssets []AdditionalAsset `json:"additionalAssets"`
			Defaults         *Defaults         `json:"defaults"`
			Type             string            `json:"type"`
			Body             string            `json:"body"`
		} `json:"check"`
	}
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeErrors(response, http.StatusBadRequest, fieldError("body", err.Error()))
		return
	}

	var problems []map[string]any
	if _, err := uuid.Parse(input.IntegrationID); err != nil {
		problems = append(problems, fieldError("integrationId", "must match format \"uuid\""))
	}
	if _, err := uuid.Parse(input.Check.CustomCheckID); err != nil {
		problems = append(problems, fieldError("check.customCheckId", "must match format \"uuid\""))
	}
	if input.Check.Target == nil || input.Check.Target.AssetType == "" {
		problems = append(problems, fieldError("check.target", "must have required property"))
	}
	if input.Check.Body == "" {
		problems = append(problems, fieldError("check.body", "must have required property"))
	}
	if input.Check.Defaults != nil && input.Check.Defaults.Informational == nil {
		problems = append(problems, fieldError("check.defaults.informational", "must have required property"))
	}
	if len(problems) > 0 {
		writeErrors(response, http.StatusBadRequest, problems...)
		return
	}

	run := &dryRun{id: uuid.NewString(), checkID: input.Check.CustomCheckID, outcome: s.dryRunOutcome}
	s.dryRuns[run.id] = run
	writeJSON(response, http.StatusAccepted, map[string]any{
		"dryRunId": run.id,
		"status":   "RUNNING",
		"pollUrl":  "/v1/tenant/custom-check-dry-runs/" + run.id,
	})
}

func (s *Server) pollDryRun(response http.ResponseWriter, id string) {
	run, ok := s.dryRuns[id]
	if !ok {
		writeErrors(response, http.StatusNotFound, map[string]any{"message": "Dry run not found"})
		return
	}

	body := map[string]any{"dryRunId": run.id, "startedAt": "2026-09-01T00:00:00.000Z"}
	if run.polls < run.outcome.PollsBeforeDone {
		run.polls++
		body["status"] = "RUNNING"
		writeJSON(response, http.StatusOK, body)
		return
	}

	findings := run.outcome.Findings
	if findings == nil {
		findings = []map[string]any{}
	}
	failures := run.outcome.Failures
	if failures == nil {
		failures = []map[string]any{}
	}
	body["status"] = run.outcome.Status
	body["completedAt"] = "2026-09-01T00:00:03.000Z"
	body["output"] = map[string]any{
		"totalChecks":    1,
		"succeededCount": 1 - min(run.outcome.FailedCount, 1),
		"failedCount":    run.outcome.FailedCount,
		"totalFindings":  len(findings),
		"dryRun":         true,
		"perCheck":       []map[string]any{{"customCheckId": run.checkID, "findingCount": len(findings)}},
		"failures":       failures,
	}
	body["findings"] = findings
	writeJSON(response, http.StatusOK, body)
}

func (s *Server) insert(input Writable) *Check {
	now := s.tick()
	check := &Check{
		Writable:  input,
		ID:        uuid.NewString(),
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.checks[check.ID] = check
	s.order = append(s.order, check.ID)
	return check
}

func (s *Server) remove(id string) {
	delete(s.checks, id)
	for index, existing := range s.order {
		if existing == id {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
}

func (s *Server) slugTaken(slug, exceptID string) bool {
	for id, check := range s.checks {
		if check.Slug == slug && id != exceptID {
			return true
		}
	}
	return false
}

func (s *Server) tick() string {
	s.clock = s.clock.Add(time.Second)
	return s.clock.Format("2006-01-02T15:04:05.000Z")
}

func decodeWritable(response http.ResponseWriter, request *http.Request) (Writable, bool) {
	var input Writable
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	// Numbers decode as float64, like the real API, so they come back the way
	// JavaScript writes them: 0.0000005 as 5e-7, and rounded to 64-bit floats.
	if err := decoder.Decode(&input); err != nil {
		writeErrors(response, http.StatusBadRequest, fieldError("body", err.Error()))
		return input, false
	}

	var problems []map[string]any
	if !slugPattern.MatchString(input.Slug) || len(input.Slug) > 128 {
		problems = append(problems, fieldError("slug", "must match pattern \"^[a-z0-9-]+$\""))
	}
	if input.Title == "" {
		problems = append(problems, fieldError("title", "must have required property"))
	}
	if input.Target == nil || input.Target.AssetType == "" || (input.Target.Scope != "batch" && input.Target.Scope != "all") {
		problems = append(problems, fieldError("target", "must have required property"))
	}
	if input.Defaults == nil || input.Defaults.SeverityLevel == "" || input.Defaults.Message == "" || input.Defaults.Informational == nil {
		problems = append(problems, fieldError("defaults", "must have required property"))
	}
	if input.Type != "rego" {
		problems = append(problems, fieldError("type", "must be equal to one of the allowed values"))
	}
	if input.Body == "" {
		problems = append(problems, fieldError("body", "must have required property"))
	}
	if len(problems) > 0 {
		writeErrors(response, http.StatusBadRequest, problems...)
		return input, false
	}
	return input, true
}

// normalize makes two writable bodies comparable, the way the API compares
// an update with the stored check.
func normalize(input Writable) string {
	encoded, _ := json.Marshal(input)
	return string(encoded)
}

// render encodes a check the way the API does, with metadata keys in reverse
// order and spaced out so tests see a different string from the one sent.
func render(check *Check, withRunStatus bool) json.RawMessage {
	body := map[string]any{
		"customCheckId":       check.ID,
		"slug":                check.Slug,
		"organizationId":      organizationID,
		"tenantId":            tenantID,
		"title":               check.Title,
		"target":              check.Target,
		"type":                check.Type,
		"body":                check.Body,
		"version":             check.Version,
		"createdBy":           actorID,
		"createdAt":           check.CreatedAt,
		"updatedBy":           actorID,
		"updatedAt":           check.UpdatedAt,
		"statusByIntegration": map[string]any{},
	}
	if check.Description != nil {
		body["description"] = *check.Description
	}
	if len(check.AdditionalAssets) > 0 {
		body["additionalAssets"] = check.AdditionalAssets
	}

	defaults := map[string]any{
		"severityLevel": check.Defaults.SeverityLevel,
		"message":       check.Defaults.Message,
		"informational": *check.Defaults.Informational,
	}
	if check.Defaults.Remediation != nil {
		defaults["remediation"] = *check.Defaults.Remediation
	}
	if check.Defaults.Meta != nil {
		defaults["meta"] = scrambledJSON(check.Defaults.Meta)
	}
	body["defaults"] = defaults

	if withRunStatus {
		if check.LastRunAt != "" {
			body["lastRunAt"] = check.LastRunAt
		}
		if check.Rollup != nil {
			body["rollup"] = check.Rollup
		}
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return encoded
}

func scrambledJSON(value map[string]any) json.RawMessage {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))

	var buffer bytes.Buffer
	buffer.WriteString("{ ")
	for index, key := range keys {
		if index > 0 {
			buffer.WriteString(", ")
		}
		encodedKey, _ := json.Marshal(key)
		buffer.Write(encodedKey)
		buffer.WriteString(": ")
		if nested, isMap := value[key].(map[string]any); isMap {
			buffer.Write(scrambledJSON(nested))
			continue
		}
		encodedValue, _ := json.Marshal(value[key])
		buffer.Write(encodedValue)
	}
	buffer.WriteString(" }")
	return buffer.Bytes()
}

func fieldError(field, message string) map[string]any {
	return map[string]any{"field": field, "code": "InvalidBody", "message": message}
}

func writeErrors(response http.ResponseWriter, status int, errors ...map[string]any) {
	writeJSON(response, status, map[string]any{"errors": errors})
}

func writeJSON(response http.ResponseWriter, status int, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	writeRaw(response, status, encoded)
}

func writeRaw(response http.ResponseWriter, status int, body []byte) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
