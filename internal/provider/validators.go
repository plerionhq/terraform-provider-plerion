package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9-]+$`)
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// maxSafeInteger is the largest integer a JavaScript number holds exactly.
// The API rejects larger whole numbers in metadata.
const maxSafeInteger = 1<<53 - 1

func slugValidators() []validator.String {
	return []validator.String{
		stringvalidator.LengthBetween(1, 128),
		stringvalidator.RegexMatches(slugPattern, "must contain only lowercase letters, numbers and hyphens"),
	}
}

// jsonObjectValidator checks that a string holds a JSON object the API can
// store as finding metadata.
type jsonObjectValidator struct{}

func (v jsonObjectValidator) Description(_ context.Context) string {
	return "value must be a JSON object"
}

func (v jsonObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v jsonObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	var decoded any
	if err := decodeWithNumbers([]byte(req.ConfigValue.ValueString()), &decoded); err != nil {
		// The jsontypes.Normalized type reports syntax errors itself.
		return
	}

	if _, isObject := decoded.(map[string]any); !isObject {
		resp.Diagnostics.AddAttributeError(req.Path, "Metadata must be a JSON object",
			"Set this attribute with jsonencode() on a map or object, for example jsonencode({ owner = \"platform-team\" }).")
		return
	}

	if number, found := findUnsafeInteger(decoded); found {
		resp.Diagnostics.AddAttributeError(req.Path, "Metadata contains a number that is too large",
			fmt.Sprintf("The number %s is larger than Plerion can store exactly (the limit is %d). "+
				"Store large identifiers, such as account numbers, as strings instead.", number, int64(maxSafeInteger)))
	}
}

// decodeWithNumbers decodes JSON keeping each number's exact text.
func decodeWithNumbers(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}

// findUnsafeInteger reports a number that becomes a whole number above the
// safe limit once stored as a 64-bit float, which is what the API rejects.
func findUnsafeInteger(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if number, found := findUnsafeInteger(child); found {
				return number, true
			}
		}
	case []any:
		for _, child := range typed {
			if number, found := findUnsafeInteger(child); found {
				return number, true
			}
		}
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		if err == nil && parsed == math.Trunc(parsed) && math.Abs(parsed) > maxSafeInteger {
			return typed.String(), true
		}
	}
	return "", false
}
