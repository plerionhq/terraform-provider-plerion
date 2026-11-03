package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// The tenant and AWS onboarding routes. Unlike the custom check routes, they
// wrap every answer in a data field. An account in HeldAccountID belongs to an
// AWS organization the tenant hasn't seen, so the fake holds it for approval
// the way Plerion does. A role whose name starts with "not-ready" fails to
// assume once, the way a just-created IAM role does.
const (
	TenantID          = tenantID
	ExternalID        = "00000000-0000-4000-8000-00000000000d"
	HeldAccountID     = "222222222222"
	AWSOrganizationID = "o-abcdefghij"

	statusActive          = "active"
	statusPendingApproval = "pending_approval"
)

var roleAccountPattern = regexp.MustCompile(`^arn:aws:iam::(\d{12}):`)

// AWSIntegration mirrors the API's AWS integration.
type AWSIntegration struct {
	IntegrationID               string `json:"integrationId"`
	AccountID                   string `json:"accountId"`
	Name                        string `json:"name"`
	RoleARN                     string `json:"roleArn"`
	EnvironmentID               string `json:"environmentId,omitempty"`
	InstanceProfileARN          string `json:"instanceProfileArn,omitempty"`
	CWPPKMSAccessMode           string `json:"cwppKMSAccessMode,omitempty"`
	ServiceAccountID            string `json:"serviceAccountId,omitempty"`
	ServiceAccountAccessRoleARN string `json:"serviceAccountAccessRoleArn,omitempty"`
	ServiceAccountType          string `json:"serviceAccountType,omitempty"`
	Status                      string `json:"status"`
}

// ServiceAccount mirrors the API's AWS service account.
type ServiceAccount struct {
	AccountID                   string `json:"accountId"`
	Name                        string `json:"name"`
	ControlPlaneRoleARN         string `json:"controlPlaneRoleArn"`
	ApplianceInstanceProfileARN string `json:"applianceInstanceProfileArn"`
	Status                      string `json:"status"`
	Enabled                     bool   `json:"enabled"`
	AWSOrganizationID           string `json:"awsOrganizationId,omitempty"`
	TemplateVersion             string `json:"templateVersion,omitempty"`
}

// ServiceAccountRegion mirrors the API's service account region.
type ServiceAccountRegion struct {
	AccountID        string `json:"accountId"`
	Region           string `json:"region"`
	VPCID            string `json:"vpcId"`
	SubnetID         string `json:"subnetId,omitempty"`
	SecurityGroupID  string `json:"securityGroupId"`
	QueueURL         string `json:"queueUrl"`
	MaxInstanceLimit int64  `json:"maxInstanceLimit"`
	Enabled          bool   `json:"enabled"`
	TemplateVersion  string `json:"templateVersion,omitempty"`
}

// The request bodies the API accepts. Like Plerion, which validates every body
// with additionalProperties: false, decodeBody refuses a field not listed here.
type awsIntegrationRequest struct {
	RoleARN                     string `json:"roleArn"`
	Name                        string `json:"name"`
	EnvironmentID               string `json:"environmentId"`
	InstanceProfileARN          string `json:"instanceProfileArn"`
	CWPPKMSAccessMode           string `json:"cwppKMSAccessMode"`
	ServiceAccountID            string `json:"serviceAccountId"`
	ServiceAccountAccessRoleARN string `json:"serviceAccountAccessRoleArn"`
	ServiceAccountType          string `json:"serviceAccountType"`
}

type serviceAccountRequest struct {
	ControlPlaneRoleARN         string `json:"controlPlaneRoleArn"`
	ApplianceInstanceProfileARN string `json:"applianceInstanceProfileArn"`
	Name                        string `json:"name"`
	TemplateVersion             string `json:"templateVersion"`
}

type serviceAccountRegionRequest struct {
	VPCID            string `json:"vpcId"`
	SubnetID         string `json:"subnetId"`
	SecurityGroupID  string `json:"securityGroupId"`
	QueueURL         string `json:"queueUrl"`
	MaxInstanceLimit int64  `json:"maxInstanceLimit"`
	TemplateVersion  string `json:"templateVersion"`
}

type awsState struct {
	integrations       map[string]*AWSIntegration
	integrationCreates int
	deleting           map[string]bool
	failDeletes        map[string]bool
	serviceAccounts    map[string]*ServiceAccount
	regions            map[string]*ServiceAccountRegion
	notReadySeen       map[string]bool
}

func newAWSState() awsState {
	return awsState{
		integrations:    map[string]*AWSIntegration{},
		deleting:        map[string]bool{},
		failDeletes:     map[string]bool{},
		serviceAccounts: map[string]*ServiceAccount{},
		regions:         map[string]*ServiceAccountRegion{},
		notReadySeen:    map[string]bool{},
	}
}

// RenameAWSIntegration changes an integration's name as if someone renamed it
// in the console.
func (s *Server) RenameAWSIntegration(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aws.integrations[id].Name = name
}

// SetAWSIntegrationEnvironment moves an integration to another environment as
// if someone did it in the console.
func (s *Server) SetAWSIntegrationEnvironment(id, environmentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aws.integrations[id].EnvironmentID = environmentID
}

// FailAWSIntegrationDeletes makes Plerion's background deletion of an
// integration fail, or succeed again when fail is false. A failed deletion
// leaves the integration in place with status delete_failed, as in Plerion.
func (s *Server) FailAWSIntegrationDeletes(id string, fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aws.failDeletes[id] = fail
}

// DisableServiceAccountRegion turns a region off as if someone did it in the console.
func (s *Server) DisableServiceAccountRegion(accountID, region string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aws.regions[accountID+"/"+region].Enabled = false
}

// SeedAWSIntegration stores an integration as if someone connected the
// account outside Terraform, and returns its id, a UUID as in Plerion.
func (s *Server) SeedAWSIntegration(accountID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := uuid.NewString()
	s.aws.integrations[id] = &AWSIntegration{
		IntegrationID: id,
		AccountID:     accountID,
		Name:          accountID,
		RoleARN:       "arn:aws:iam::" + accountID + ":role/Existing",
		Status:        statusActive,
	}
	return id
}

// AWSCounts reports how many integrations, integration creates, service
// accounts and regions the fake holds.
func (s *Server) AWSCounts() (integrations, creates, serviceAccounts, regions int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.aws.integrations), s.aws.integrationCreates, len(s.aws.serviceAccounts), len(s.aws.regions)
}

// handleAWSOnboarding answers the tenant and AWS onboarding routes, and
// reports whether the request was one of them.
func (s *Server) handleAWSOnboarding(response http.ResponseWriter, request *http.Request, segments []string) bool {
	if len(segments) < 2 || segments[0] != "v1" || segments[1] != "tenant" {
		return false
	}
	rest, method := segments[2:], request.Method

	switch {
	case len(rest) == 0 && method == http.MethodGet:
		writeData(response, http.StatusOK, map[string]string{
			"tenantId": tenantID, "organizationId": organizationID, "name": "Test tenant",
		})
	case len(rest) == 1 && rest[0] == "external-id" && method == http.MethodGet:
		writeData(response, http.StatusOK, map[string]string{"externalId": ExternalID})
	case len(rest) >= 2 && rest[0] == "integrations" && rest[1] == "aws":
		s.routeAWSIntegrations(response, request, rest[2:])
	case len(rest) >= 1 && rest[0] == "service-accounts":
		s.routeServiceAccounts(response, request, rest[1:])
	default:
		return false
	}
	return true
}

func (s *Server) routeAWSIntegrations(response http.ResponseWriter, request *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && request.Method == http.MethodPost:
		s.createAWSIntegration(response, request)
	case len(rest) == 1 && request.Method == http.MethodGet:
		s.getAWSIntegration(response, rest[0])
	case len(rest) == 1 && request.Method == http.MethodPatch:
		s.patchAWSIntegration(response, request, rest[0])
	case len(rest) == 1 && request.Method == http.MethodDelete:
		s.deleteAWSIntegration(response, rest[0])
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (s *Server) routeServiceAccounts(response http.ResponseWriter, request *http.Request, rest []string) {
	switch {
	case len(rest) == 0 && request.Method == http.MethodPost:
		s.createServiceAccount(response, request)
	case len(rest) == 1 && request.Method == http.MethodGet:
		s.getServiceAccount(response, rest[0])
	case len(rest) == 1 && request.Method == http.MethodPatch:
		s.patchServiceAccount(response, request, rest[0])
	case len(rest) == 1 && request.Method == http.MethodDelete:
		s.deleteServiceAccount(response, rest[0])
	case len(rest) == 3 && rest[1] == "regions" && request.Method == http.MethodPut:
		s.putRegion(response, request, rest[0], rest[2])
	case len(rest) == 3 && rest[1] == "regions" && request.Method == http.MethodGet:
		s.getRegion(response, rest[0], rest[2])
	case len(rest) == 3 && rest[1] == "regions" && request.Method == http.MethodDelete:
		s.deleteRegion(response, rest[0], rest[2])
	default:
		writeJSON(response, http.StatusNotFound, map[string]any{"message": "Not Found"})
	}
}

func (s *Server) createAWSIntegration(response http.ResponseWriter, request *http.Request) {
	var input awsIntegrationRequest
	if !decodeBody(response, request, &input) {
		return
	}
	// Plerion looks for an existing record before it assumes the role.
	accountID := accountOf(input.RoleARN)
	for _, existing := range s.aws.integrations {
		if existing.AccountID == accountID {
			writeCodedError(response, http.StatusConflict, "IntegrationAlreadyExists",
				fmt.Sprintf("AWS account %s already has integration %s in this tenant.", accountID, existing.IntegrationID))
			return
		}
	}
	if !s.assumable(response, input.RoleARN) {
		return
	}

	s.aws.integrationCreates++
	integration := AWSIntegration{
		IntegrationID:               "int-" + accountID,
		AccountID:                   accountID,
		Name:                        input.Name,
		RoleARN:                     input.RoleARN,
		EnvironmentID:               input.EnvironmentID,
		InstanceProfileARN:          input.InstanceProfileARN,
		CWPPKMSAccessMode:           input.CWPPKMSAccessMode,
		ServiceAccountID:            input.ServiceAccountID,
		ServiceAccountAccessRoleARN: input.ServiceAccountAccessRoleARN,
		ServiceAccountType:          input.ServiceAccountType,
		Status:                      statusFor(accountID),
	}
	if integration.Name == "" {
		integration.Name = accountID
	}
	s.aws.integrations[integration.IntegrationID] = &integration
	writeData(response, http.StatusCreated, integration)
}

func (s *Server) getAWSIntegration(response http.ResponseWriter, id string) {
	integration, ok := s.aws.integrations[id]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Integration not found")
		return
	}
	// Plerion removes an integration in the background: answer once more, then 404.
	if s.aws.deleting[id] {
		delete(s.aws.integrations, id)
		delete(s.aws.deleting, id)
	}
	writeData(response, http.StatusOK, integration)
}

func (s *Server) patchAWSIntegration(response http.ResponseWriter, request *http.Request, id string) {
	integration, ok := s.aws.integrations[id]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Integration not found")
		return
	}
	var changes map[string]*string
	if !decodeBody(response, request, &changes) {
		return
	}
	if len(changes) == 0 {
		writeCodedError(response, http.StatusBadRequest, "BadRequest", "Nothing to update")
		return
	}

	// A null clears the setting, where the API allows one.
	for key, value := range changes {
		text := ""
		if value != nil {
			text = *value
		} else if !clearableIntegrationFields[key] {
			writeCodedError(response, http.StatusBadRequest, "BadRequest", key+" can't be null")
			return
		}
		switch key {
		case "roleArn":
			if accountOf(text) != integration.AccountID {
				writeCodedError(response, http.StatusBadRequest, "BadRequest", "roleArn must stay in the same AWS account")
				return
			}
			if !s.assumable(response, text) {
				return
			}
			integration.RoleARN = text
		case "name":
			integration.Name = text
		case "environmentId":
			integration.EnvironmentID = text
		case "instanceProfileArn":
			integration.InstanceProfileARN = text
		case "cwppKMSAccessMode":
			integration.CWPPKMSAccessMode = text
		case "serviceAccountId":
			integration.ServiceAccountID = text
		case "serviceAccountAccessRoleArn":
			integration.ServiceAccountAccessRoleARN = text
		default:
			writeCodedError(response, http.StatusBadRequest, "BadRequest", key+" can't be changed")
			return
		}
	}
	writeData(response, http.StatusOK, integration)
}

// clearableIntegrationFields are the PATCH fields the API lets a null clear.
var clearableIntegrationFields = map[string]bool{
	"environmentId":               true,
	"instanceProfileArn":          true,
	"serviceAccountId":            true,
	"serviceAccountAccessRoleArn": true,
}

func (s *Server) deleteAWSIntegration(response http.ResponseWriter, id string) {
	integration, ok := s.aws.integrations[id]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Integration not found")
		return
	}
	// Plerion removes the integration in the background. A removal that fails
	// leaves it delete_failed, and the next DELETE starts it again.
	if s.aws.failDeletes[id] {
		integration.Status = "delete_failed"
	} else {
		integration.Status = "delete_in_progress"
		s.aws.deleting[id] = true
	}
	writeData(response, http.StatusAccepted, map[string]string{"integrationId": id, "status": "delete_in_progress"})
}

func (s *Server) createServiceAccount(response http.ResponseWriter, request *http.Request) {
	var input serviceAccountRequest
	if !decodeBody(response, request, &input) {
		return
	}
	accountID := accountOf(input.ControlPlaneRoleARN)
	if _, ok := s.aws.serviceAccounts[accountID]; ok {
		writeCodedError(response, http.StatusConflict, "ServiceAccountAlreadyExists",
			fmt.Sprintf("AWS account %s is already a service account in this tenant.", accountID))
		return
	}
	if !s.assumable(response, input.ControlPlaneRoleARN) {
		return
	}

	serviceAccount := ServiceAccount{
		AccountID:                   accountID,
		Name:                        input.Name,
		ControlPlaneRoleARN:         input.ControlPlaneRoleARN,
		ApplianceInstanceProfileARN: input.ApplianceInstanceProfileARN,
		TemplateVersion:             input.TemplateVersion,
		Status:                      statusFor(accountID),
	}
	serviceAccount.Enabled = serviceAccount.Status == statusActive
	serviceAccount.AWSOrganizationID = AWSOrganizationID
	if serviceAccount.Name == "" {
		serviceAccount.Name = accountID
	}
	s.aws.serviceAccounts[accountID] = &serviceAccount
	writeData(response, http.StatusCreated, serviceAccount)
}

func (s *Server) getServiceAccount(response http.ResponseWriter, accountID string) {
	serviceAccount, ok := s.aws.serviceAccounts[accountID]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Service account not found")
		return
	}
	writeData(response, http.StatusOK, serviceAccount)
}

func (s *Server) patchServiceAccount(response http.ResponseWriter, request *http.Request, accountID string) {
	serviceAccount, ok := s.aws.serviceAccounts[accountID]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Service account not found")
		return
	}
	var changes map[string]*string
	if !decodeBody(response, request, &changes) {
		return
	}
	for key, pointer := range changes {
		if pointer == nil {
			writeCodedError(response, http.StatusBadRequest, "BadRequest", key+" can't be null")
			return
		}
		value := *pointer
		switch key {
		case "controlPlaneRoleArn":
			serviceAccount.ControlPlaneRoleARN = value
		case "applianceInstanceProfileArn":
			serviceAccount.ApplianceInstanceProfileARN = value
		case "name":
			serviceAccount.Name = value
		case "templateVersion":
			serviceAccount.TemplateVersion = value
		default:
			writeCodedError(response, http.StatusBadRequest, "BadRequest", key+" can't be changed")
			return
		}
	}
	writeData(response, http.StatusOK, serviceAccount)
}

func (s *Server) deleteServiceAccount(response http.ResponseWriter, accountID string) {
	if _, ok := s.aws.serviceAccounts[accountID]; !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Service account not found")
		return
	}
	delete(s.aws.serviceAccounts, accountID)
	writeData(response, http.StatusOK, map[string]string{"accountId": accountID})
}

func (s *Server) putRegion(response http.ResponseWriter, request *http.Request, accountID, region string) {
	serviceAccount, ok := s.aws.serviceAccounts[accountID]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Service account not found")
		return
	}
	if serviceAccount.Status == statusPendingApproval {
		writeCodedError(response, http.StatusConflict, "ServiceAccountPendingApproval",
			"Service account "+accountID+" is awaiting approval. Approve it in the Plerion console, then apply again.")
		return
	}
	var input serviceAccountRegionRequest
	if !decodeBody(response, request, &input) {
		return
	}
	if input.MaxInstanceLimit == 0 {
		input.MaxInstanceLimit = 10
	}

	// Like Plerion: a new region starts enabled, and a PUT keeps an existing
	// region's enabled setting, and its template version unless it sends one.
	key := accountID + "/" + region
	saved := ServiceAccountRegion{
		AccountID:        accountID,
		Region:           region,
		VPCID:            input.VPCID,
		SubnetID:         input.SubnetID,
		SecurityGroupID:  input.SecurityGroupID,
		QueueURL:         input.QueueURL,
		MaxInstanceLimit: input.MaxInstanceLimit,
		Enabled:          true,
		TemplateVersion:  input.TemplateVersion,
	}
	if existing, ok := s.aws.regions[key]; ok {
		saved.Enabled = existing.Enabled
		if saved.TemplateVersion == "" {
			saved.TemplateVersion = existing.TemplateVersion
		}
	}
	s.aws.regions[key] = &saved
	writeData(response, http.StatusOK, saved)
}

func (s *Server) getRegion(response http.ResponseWriter, accountID, region string) {
	saved, ok := s.aws.regions[accountID+"/"+region]
	if !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Region not found")
		return
	}
	writeData(response, http.StatusOK, saved)
}

func (s *Server) deleteRegion(response http.ResponseWriter, accountID, region string) {
	key := accountID + "/" + region
	if _, ok := s.aws.regions[key]; !ok {
		writeCodedError(response, http.StatusNotFound, "NotFound", "Region not found")
		return
	}
	delete(s.aws.regions, key)
	writeData(response, http.StatusOK, map[string]string{"region": region})
}

// assumable fails the first attempt for a role named not-ready*.
func (s *Server) assumable(response http.ResponseWriter, roleARN string) bool {
	if strings.Contains(roleARN, ":role/not-ready") && !s.aws.notReadySeen[roleARN] {
		s.aws.notReadySeen[roleARN] = true
		writeCodedError(response, http.StatusUnprocessableEntity, "RoleNotAssumable", "Plerion could not assume "+roleARN+" yet.")
		return false
	}
	return true
}

func statusFor(accountID string) string {
	if accountID == HeldAccountID {
		return statusPendingApproval
	}
	return statusActive
}

func accountOf(arn string) string {
	if match := roleAccountPattern.FindStringSubmatch(arn); match != nil {
		return match[1]
	}
	return ""
}

func decodeBody(response http.ResponseWriter, request *http.Request, out any) bool {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeCodedError(response, http.StatusBadRequest, "BadRequest", err.Error())
		return false
	}
	return true
}

func writeData(response http.ResponseWriter, status int, data any) {
	writeJSON(response, status, map[string]any{"data": data})
}

func writeCodedError(response http.ResponseWriter, status int, code, message string) {
	writeErrors(response, status, map[string]any{"code": code, "message": message})
}
