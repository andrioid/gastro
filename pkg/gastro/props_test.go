package gastro_test

import (
	"testing"

	"github.com/andrioid/gastro/pkg/gastro"
)

func TestMapToStruct_StringFields(t *testing.T) {
	type Props struct {
		Title string
		Body  string
	}

	m := map[string]any{
		"Title": "Hello",
		"Body":  "World",
	}

	result, err := gastro.MapToStruct[Props](m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Title != "Hello" {
		t.Errorf("Title: got %q, want %q", result.Title, "Hello")
	}
	if result.Body != "World" {
		t.Errorf("Body: got %q, want %q", result.Body, "World")
	}
}

func TestMapToStruct_BoolField(t *testing.T) {
	type Props struct {
		Urgent bool
	}

	tests := []struct {
		name  string
		input any
		want  bool
	}{
		{"bool true", true, true},
		{"bool false", false, false},
		{"string true", "true", true},
		{"string false", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{"Urgent": tt.input}
			result, err := gastro.MapToStruct[Props](m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Urgent != tt.want {
				t.Errorf("Urgent: got %v, want %v", result.Urgent, tt.want)
			}
		})
	}
}

func TestMapToStruct_IntField(t *testing.T) {
	type Props struct {
		Count int
	}

	tests := []struct {
		name  string
		input any
		want  int
	}{
		{"int value", 42, 42},
		{"string value", "42", 42},
		{"float64 value", float64(42), 42},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := map[string]any{"Count": tt.input}
			result, err := gastro.MapToStruct[Props](m)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Count != tt.want {
				t.Errorf("Count: got %d, want %d", result.Count, tt.want)
			}
		})
	}
}

func TestMapToStruct_StructField(t *testing.T) {
	type Inner struct {
		Name string
	}
	type Props struct {
		Item Inner
	}

	m := map[string]any{
		"Item": Inner{Name: "hello"},
	}

	result, err := gastro.MapToStruct[Props](m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Item.Name != "hello" {
		t.Errorf("Item.Name: got %q, want %q", result.Item.Name, "hello")
	}
}

func TestMapToStruct_SliceField(t *testing.T) {
	type Props struct {
		Items []string
	}

	items := []string{"a", "b", "c"}
	m := map[string]any{
		"Items": items,
	}

	result, err := gastro.MapToStruct[Props](m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Items) != 3 {
		t.Fatalf("Items length: got %d, want 3", len(result.Items))
	}
	if result.Items[0] != "a" {
		t.Errorf("Items[0]: got %q, want %q", result.Items[0], "a")
	}
}

func TestMapToStruct_MissingFieldIgnored(t *testing.T) {
	type Props struct {
		Title string
		Body  string
	}

	// Only Title is provided, Body should be zero value
	m := map[string]any{
		"Title": "Hello",
	}

	result, err := gastro.MapToStruct[Props](m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Title != "Hello" {
		t.Errorf("Title: got %q, want %q", result.Title, "Hello")
	}
	if result.Body != "" {
		t.Errorf("Body: got %q, want empty string", result.Body)
	}
}

func TestMapToStruct_TypeMismatchReturnsError(t *testing.T) {
	type Props struct {
		Count int
	}

	m := map[string]any{
		"Count": "not-a-number",
	}

	_, err := gastro.MapToStruct[Props](m)
	if err == nil {
		t.Fatal("expected an error for type mismatch, got nil")
	}
}

func TestMapToStruct_EmptyMap(t *testing.T) {
	type Props struct {
		Title string
	}

	result, err := gastro.MapToStruct[Props](map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Title != "" {
		t.Errorf("Title should be zero value, got %q", result.Title)
	}
}
