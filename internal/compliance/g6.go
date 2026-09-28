package compliance

import (
	"context"
	"fmt"
)

// G6 Format variety: same format ≤2× in a row; hook style not 3× in a row.
// Picker enforces at write time; this gate verifies the chosen format/hook.
type G6 struct{}

func (g G6) ID() string { return "G6" }

func (g G6) Check(ctx context.Context, item ContentItem) GateResult {
	_ = ctx
	recent := item.Recent
	format := item.Format
	hook := item.HookStyle

	if format != "" && len(recent) >= 2 &&
		recent[0].Format != "" &&
		recent[0].Format == recent[1].Format &&
		format == recent[0].Format {
		return GateResult{
			ID:     "G6",
			Passed: false,
			Detail: fmt.Sprintf("format %q used more than 2× in a row", format),
		}
	}
	if hook != "" && len(recent) >= 3 &&
		recent[0].HookStyle != "" &&
		recent[0].HookStyle == recent[1].HookStyle &&
		recent[1].HookStyle == recent[2].HookStyle &&
		hook == recent[0].HookStyle {
		return GateResult{
			ID:     "G6",
			Passed: false,
			Detail: fmt.Sprintf("hook style %q repeated 3× in a row", hook),
		}
	}
	return GateResult{ID: "G6", Passed: true, Detail: "format/hook variety ok"}
}
