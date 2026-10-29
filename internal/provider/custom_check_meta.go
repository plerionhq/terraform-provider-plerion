package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// metaType is a JSON string compared by value the way the API stores it: the
// API keeps numbers as 64-bit floats, so it returns 0.0000005 as 5e-7 and
// rounds 0.12345678901234567 to 0.12345678901234566. jsontypes.Normalized
// compares number text, which would report those as changes.
type metaType struct {
	jsontypes.NormalizedType
}

var (
	_ basetypes.StringTypable                    = metaType{}
	_ basetypes.StringValuableWithSemanticEquals = metaValue{}
)

func (t metaType) String() string {
	return "provider.metaType"
}

func (t metaType) ValueType(_ context.Context) attr.Value {
	return metaValue{}
}

func (t metaType) Equal(other attr.Type) bool {
	_, ok := other.(metaType)
	return ok
}

func (t metaType) ValueFromString(_ context.Context, in basetypes.StringValue) (basetypes.StringValuable, diag.Diagnostics) {
	return metaValue{Normalized: jsontypes.Normalized{StringValue: in}}, nil
}

func (t metaType) ValueFromTerraform(ctx context.Context, in tftypes.Value) (attr.Value, error) {
	attrValue, err := t.StringType.ValueFromTerraform(ctx, in)
	if err != nil {
		return nil, err
	}
	stringValue, ok := attrValue.(basetypes.StringValue)
	if !ok {
		return nil, fmt.Errorf("unexpected value type %T", attrValue)
	}
	return metaValue{Normalized: jsontypes.Normalized{StringValue: stringValue}}, nil
}

type metaValue struct {
	jsontypes.Normalized
}

func newMetaNull() metaValue {
	return metaValue{Normalized: jsontypes.NewNormalizedNull()}
}

func newMetaValue(value string) metaValue {
	return metaValue{Normalized: jsontypes.NewNormalizedValue(value)}
}

func (v metaValue) Type(_ context.Context) attr.Type {
	return metaType{}
}

func (v metaValue) Equal(other attr.Value) bool {
	otherValue, ok := other.(metaValue)
	return ok && v.StringValue.Equal(otherValue.StringValue)
}

func (v metaValue) StringSemanticEquals(_ context.Context, newValuable basetypes.StringValuable) (bool, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	newValue, ok := newValuable.(metaValue)
	if !ok {
		diagnostics.AddError("Semantic equality check error",
			fmt.Sprintf("Expected a metaValue but got %T. This is a bug in the provider; please report it.", newValuable))
		return false, diagnostics
	}
	return jsonValuesEqual(v.ValueString(), newValue.ValueString()), diagnostics
}

// jsonValuesEqual decodes numbers as float64, as the API does, so two
// documents are equal when the API would store the same values.
func jsonValuesEqual(first, second string) bool {
	var firstDecoded, secondDecoded any
	if json.Unmarshal([]byte(first), &firstDecoded) != nil || json.Unmarshal([]byte(second), &secondDecoded) != nil {
		return first == second
	}
	return reflect.DeepEqual(firstDecoded, secondDecoded)
}

// canonicalJSON rewrites JSON with sorted keys, no whitespace, and numbers
// written out in full without exponents, which is the form jsonencode()
// produces. That keeps imported checks free of diffs.
func canonicalJSON(raw []byte) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// UseNumber keeps large integers exact instead of rounding them to float64.
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return string(raw)
	}
	canonical, err := json.Marshal(canonicalNumbers(decoded))
	if err != nil {
		return string(raw)
	}
	return string(canonical)
}

func canonicalNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			typed[key] = canonicalNumbers(child)
		}
	case []any:
		for index, child := range typed {
			typed[index] = canonicalNumbers(child)
		}
	case json.Number:
		if !strings.ContainsAny(typed.String(), ".eE") {
			return typed
		}
		// Terraform parses number literals at 512 bits of precision.
		parsed, _, err := big.ParseFloat(typed.String(), 10, 512, big.ToNearestEven)
		if err != nil {
			return typed
		}
		return json.Number(parsed.Text('f', -1))
	}
	return value
}
