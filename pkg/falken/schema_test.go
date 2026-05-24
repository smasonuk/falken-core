package falken

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaBuilderBasicObject(t *testing.T) {
	schema := Object(
		Required("email", String().Description("User email address").Format("email")),
		Optional("age", Integer()),
	)
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	if got := stringSliceFromAny(decoded["required"]); !reflect.DeepEqual(got, []string{"email"}) {
		t.Fatalf("required = %v, want email", got)
	}
	email := decoded["properties"].(map[string]any)["email"].(map[string]any)
	if email["description"] != "User email address" || email["format"] != "email" {
		t.Fatalf("email schema = %+v, want description and format", email)
	}
}

func TestSchemaBuilderNestedObject(t *testing.T) {
	schema := Object(Required("profile", Object(Required("name", String()))))
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	profile := decoded["properties"].(map[string]any)["profile"].(map[string]any)
	if profile["type"] != "object" {
		t.Fatalf("profile schema = %+v, want object", profile)
	}
	if got := stringSliceFromAny(profile["required"]); !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("nested required = %v, want name", got)
	}
}

func TestSchemaBuilderArray(t *testing.T) {
	schema := Object(Required("tags", Array(String())))
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	tags := decoded["properties"].(map[string]any)["tags"].(map[string]any)
	if tags["type"] != "array" {
		t.Fatalf("tags schema = %+v, want array", tags)
	}
	items := tags["items"].(map[string]any)
	if items["type"] != "string" {
		t.Fatalf("array items = %+v, want string", items)
	}
}

func TestSchemaBuilderEnum(t *testing.T) {
	schema := Object(Required("size", Enum("small", "medium", "large")))
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	size := decoded["properties"].(map[string]any)["size"].(map[string]any)
	if got := stringSliceFromAny(size["enum"]); !reflect.DeepEqual(got, []string{"small", "medium", "large"}) {
		t.Fatalf("enum = %v, want sizes", got)
	}
}

func TestSchemaBuilderNoArgumentObject(t *testing.T) {
	schema := Object()
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	if properties := decoded["properties"].(map[string]any); len(properties) != 0 {
		t.Fatalf("properties = %+v, want empty", properties)
	}
}

func TestSchemaForSimpleRequiredString(t *testing.T) {
	type args struct {
		Email string `json:"email" falken:"required,description=User email address,format=email"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	assertDescriptorSchemaValid(t, schema)

	decoded := decodeSchemaObject(t, schema)
	if got := stringSliceFromAny(decoded["required"]); !reflect.DeepEqual(got, []string{"email"}) {
		t.Fatalf("required = %v, want email", got)
	}
	email := decoded["properties"].(map[string]any)["email"].(map[string]any)
	if email["type"] != "string" || email["description"] != "User email address" || email["format"] != "email" {
		t.Fatalf("email schema = %+v", email)
	}
}

func TestSchemaForOptionalPointerFields(t *testing.T) {
	type args struct {
		Limit *int    `json:"limit"`
		Query *string `json:"query"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	decoded := decodeSchemaObject(t, schema)
	if _, ok := decoded["required"]; ok {
		t.Fatalf("required present in pointer-only schema: %+v", decoded["required"])
	}
	props := decoded["properties"].(map[string]any)
	if props["limit"].(map[string]any)["type"] != "integer" || props["query"].(map[string]any)["type"] != "string" {
		t.Fatalf("pointer props = %+v", props)
	}
}

func TestSchemaForNestedStruct(t *testing.T) {
	type profile struct {
		Name string `json:"name" falken:"required"`
	}
	type args struct {
		Profile profile `json:"profile"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	assertDescriptorSchemaValid(t, schema)
	profileSchema := decodeSchemaObject(t, schema)["properties"].(map[string]any)["profile"].(map[string]any)
	if got := stringSliceFromAny(profileSchema["required"]); !reflect.DeepEqual(got, []string{"name"}) {
		t.Fatalf("nested required = %v, want name", got)
	}
}

func TestSchemaForSliceField(t *testing.T) {
	type args struct {
		IDs []int64 `json:"ids"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	ids := decodeSchemaObject(t, schema)["properties"].(map[string]any)["ids"].(map[string]any)
	if ids["type"] != "array" || ids["items"].(map[string]any)["type"] != "integer" {
		t.Fatalf("ids schema = %+v", ids)
	}
}

func TestSchemaForEnumTag(t *testing.T) {
	type args struct {
		Size string `json:"size" falken:"enum=small|medium|large"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	size := decodeSchemaObject(t, schema)["properties"].(map[string]any)["size"].(map[string]any)
	if got := stringSliceFromAny(size["enum"]); !reflect.DeepEqual(got, []string{"small", "medium", "large"}) {
		t.Fatalf("enum = %v, want sizes", got)
	}
}

func TestSchemaForIgnoredJSONField(t *testing.T) {
	type args struct {
		Visible string `json:"visible"`
		Secret  string `json:"-"`
	}
	schema, err := SchemaFor[args]()
	if err != nil {
		t.Fatalf("SchemaFor: %v", err)
	}
	props := decodeSchemaObject(t, schema)["properties"].(map[string]any)
	if _, ok := props["Secret"]; ok {
		t.Fatalf("ignored field was included: %+v", props)
	}
	if _, ok := props["visible"]; !ok {
		t.Fatalf("visible field missing: %+v", props)
	}
}

func TestSchemaForUnsupportedFieldTypes(t *testing.T) {
	tests := []struct {
		name string
		typ  any
	}{
		{name: "map", typ: struct {
			Values map[string]string `json:"values"`
		}{}},
		{name: "func", typ: struct {
			Callback func() `json:"callback"`
		}{}},
		{name: "chan", typ: struct {
			Events chan string `json:"events"`
		}{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := SchemaForType(reflect.TypeOf(tt.typ))
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "unsupported schema field type") {
				t.Fatalf("SchemaForType error = %v, want unsupported type", err)
			}
		})
	}
}

func TestSchemaForRecursiveType(t *testing.T) {
	type node struct {
		Next *node `json:"next"`
	}
	_, err := SchemaFor[node]()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "recursive schema type") {
		t.Fatalf("SchemaFor recursive error = %v, want recursive type", err)
	}
}

func TestSchemaForEmbeddedStructRejectedClearly(t *testing.T) {
	type Embedded struct {
		Name string `json:"name"`
	}
	type args struct {
		Embedded
	}
	_, err := SchemaFor[args]()
	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "anonymous embedded schema field") {
		t.Fatalf("SchemaFor embedded error = %v, want clear embedded-field error", err)
	}
}

func assertDescriptorSchemaValid(t *testing.T, schema Schema) {
	t.Helper()
	if err := ValidateToolDescriptor(ToolDescriptor{
		Name:        "schema_test",
		Description: "schema test tool",
		Parameters:  schema.JSON(),
	}); err != nil {
		t.Fatalf("ValidateToolDescriptor: %v", err)
	}
}

func decodeSchemaObject(t *testing.T, schema Schema) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(schema.JSON(), &decoded); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	return decoded
}

func stringSliceFromAny(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
