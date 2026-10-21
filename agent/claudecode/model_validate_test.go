package claudecode

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chenhg5/cc-connect/core"
)

func TestValidateModel_RejectsPermissionModesAndEmpty(t *testing.T) {
	a := &Agent{activeIdx: 0, providers: []core.ProviderConfig{{Models: []core.ModelOption{
		{Name: "yolo"}, {Name: "custom", Alias: "plan"},
	}}}}
	validator, ok := any(a).(core.ModelValidator)
	if !ok {
		t.Fatal("Claude must validate model selections before they are saved")
	}
	inputs := []string{"", "  ", "yolo", "full-auto", "suggest", "auto-edit"}
	for _, mode := range a.PermissionModes() {
		inputs = append(inputs, mode.Key)
	}
	for _, input := range inputs {
		for _, model := range []string{input, " " + strings.ToUpper(input) + " "} {
			t.Run(model, func(t *testing.T) {
				if got, err := validator.ValidateModel(context.Background(), model); !errors.Is(err, core.ErrUnknownModel) {
					t.Fatalf("ValidateModel(%q) = %q, %v, want ErrUnknownModel", model, got, err)
				}
			})
		}
	}
}

func TestValidateModel_CanonicalizesConfiguredAliasAndPreservesManualIDs(t *testing.T) {
	a := &Agent{activeIdx: 0, providers: []core.ProviderConfig{{Models: []core.ModelOption{
		{Name: "vendor/custom-sonnet", Alias: "custom"},
	}}}}
	validator, ok := any(a).(core.ModelValidator)
	if !ok {
		t.Fatal("Claude must implement ModelValidator")
	}
	for _, tt := range []struct{ input, want string }{
		{"custom", "vendor/custom-sonnet"},
		{" vendor/custom-sonnet ", "vendor/custom-sonnet"},
		{" claude-sonnet-4-20250514 ", "claude-sonnet-4-20250514"},
		{"sonnet", "sonnet"},
		{"manual-MixedCase-ID", "manual-MixedCase-ID"},
	} {
		t.Run(tt.input, func(t *testing.T) {
			if got, err := validator.ValidateModel(context.Background(), tt.input); err != nil || got != tt.want {
				t.Fatalf("ValidateModel(%q) = %q, %v, want %q", tt.input, got, err, tt.want)
			}
		})
	}
}
