package agent

import (
	"context"
	"io"
)

// Agent is the provider-agnostic contract every SDK-driven chat
// provider implements. There is exactly one method — Loop — because
// everything else (binary argv, env vars, output rendering) collapses
// into "run the SDK tool-use loop and emit canonical NDJSON Messages".
//
// Loop is invoked by `cmd/cli/agent.go` (phase 8) inside the nvoi
// process for one chat turn. Implementations:
//
//  1. Build the SDK client using token.
//  2. Convert spec.History (canonical Messages) into the SDK's native
//     message-history shape.
//  3. Register the tool catalog from pkg/agent/tools as SDK-native
//     tool definitions, wired to a router that dispatches each
//     tool_use back into the canonical Tool.Execute.
//  4. Drive the multi-turn loop: send → handle tool_use → re-send → ...
//     up to spec.MaxTurns turns or until stop_reason=end_turn.
//  5. Emit Messages to out via Emitter as the loop progresses (one
//     line per SDK-side event, mapped to a canonical Kind).
//  6. Return nil on end_turn; return an error (still after emitting
//     a Kind=Error Message) on any hard failure.
//
// The token is a SEPARATE argument (not a LaunchSpec field) so it
// never appears in structured logs or test fixtures that may
// serialize the spec. The cmd/cli boundary resolves the token from
// either env (ANTHROPIC_API_KEY / OPENAI_API_KEY / GEMINI_API_KEY)
// or — in store mode — the project's encrypted secrets table.
type Agent interface {
	Loop(ctx context.Context, spec LaunchSpec, token string, out io.Writer) error
}
