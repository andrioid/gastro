package gastro_test

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"strings"
	"testing"

	"github.com/andrioid/gastro/pkg/gastro"
)

type requiredCard struct {
	Title string `gastro:"required"`
	Count int    `json:"count" gastro:"required"`
	Body  string
	Tags  []string `gastro:"required"`
}

func TestCheckRequiredProps_ReportsEveryZeroRequiredField(t *testing.T) {
	err := gastro.CheckRequiredProps(requiredCard{Body: "set"})
	var reqErr *gastro.RequiredPropsError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected *RequiredPropsError, got %v", err)
	}
	want := []string{"Title", "Count", "Tags"}
	if strings.Join(reqErr.Fields, ",") != strings.Join(want, ",") {
		t.Fatalf("Fields = %v, want %v", reqErr.Fields, want)
	}
	if !strings.Contains(err.Error(), `"Title"`) {
		t.Errorf("message should name the field: %v", err)
	}
}

func TestCheckRequiredProps_SetRequiredFieldsPass(t *testing.T) {
	props := requiredCard{Title: "t", Count: 1, Tags: []string{"a"}}
	if err := gastro.CheckRequiredProps(props); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if err := gastro.CheckRequiredProps(&props); err != nil {
		t.Fatalf("pointer: expected nil, got %v", err)
	}
}

func TestCheckRequiredProps_NonRequiredZeroFieldsPass(t *testing.T) {
	type optional struct {
		Title string
		Count int `json:"count"`
	}
	if err := gastro.CheckRequiredProps(optional{}); err != nil {
		t.Fatalf("optional fields must never fail, got %v", err)
	}
}

func TestCheckRequiredProps_NotAStruct(t *testing.T) {
	if err := gastro.CheckRequiredProps(42); err == nil {
		t.Fatal("expected an error for a non-struct value")
	}
}

type typedProps struct {
	Title   string
	Count   int
	Size    int64
	Ratio   float64
	Active  bool
	Body    template.HTML
	Tags    []string
	Any     any
	Attrs   gastro.Attrs
	Pointer *string
}

func TestCheckPropTypes_AcceptsAssignableValues(t *testing.T) {
	m := map[string]any{
		"Title":    "hi",
		"Count":    3,
		"Size":     3, // template number literals arrive as int
		"Ratio":    2, // exact integer into float
		"Active":   true,
		"Body":     "<b>x</b>", // string literal into a string-kinded type
		"Tags":     []string{"a"},
		"Any":      struct{}{},
		"Pointer":  nil,
		"Children": template.HTML("x"),
		"class":    "rest-bag key",
	}
	if err := gastro.CheckPropTypes[typedProps](m); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestCheckPropTypes_RejectsCoercions(t *testing.T) {
	cases := map[string]any{
		"Count":  "three",
		"Size":   2.5,
		"Ratio":  "1.5",
		"Active": "true",
		"Title":  42,
		"Tags":   "a",
	}
	for field, val := range cases {
		t.Run(field, func(t *testing.T) {
			err := gastro.CheckPropTypes[typedProps](map[string]any{field: val})
			var typeErr *gastro.PropTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("expected *PropTypeError for %s=%#v, got %v", field, val, err)
			}
			if !strings.Contains(err.Error(), `"`+field+`"`) {
				t.Errorf("message should name the field: %v", err)
			}
		})
	}
}

func TestCheckPropTypes_NilIntoNonNilableIsMismatch(t *testing.T) {
	if err := gastro.CheckPropTypes[typedProps](map[string]any{"Title": nil}); err == nil {
		t.Fatal("nil into string should be a mismatch")
	}
}

func TestPropsViolation_DevModeReturnsError(t *testing.T) {
	cause := gastro.CheckRequiredProps(requiredCard{})
	err := gastro.PropsViolation(true, "Card", cause)
	if err == nil {
		t.Fatal("dev mode must return the violation as an error")
	}
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "Card") {
		t.Fatalf("error should wrap the cause and name the component: %v", err)
	}
}

func TestPropsViolation_ProductionLogsAndContinues(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	cause := gastro.CheckRequiredProps(requiredCard{})
	if err := gastro.PropsViolation(false, "Card", cause); err != nil {
		t.Fatalf("production must not return an error, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "Card") || !strings.Contains(out, "Title") {
		t.Fatalf("expected a slog warning naming component and field, got %q", out)
	}
}

func TestPropsViolation_NilErrorIsNoop(t *testing.T) {
	if err := gastro.PropsViolation(true, "Card", nil); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}
