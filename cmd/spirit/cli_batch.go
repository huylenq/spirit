package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/huylenq/spirit/internal/batch"
	"github.com/huylenq/spirit/internal/daemon"
	"github.com/huylenq/spirit/internal/runbook"
	"github.com/huylenq/spirit/internal/scripting"
)

// Batch + runbook CLI verbs (W8): the same internal/batch schema and
// internal/runbook pipeline every other surface uses. `plan` previews and
// executes nothing; `action` executes with per-step receipts and
// stop-on-failure remainder semantics; `runbook` drives explain/plan/run.

// readBatchArg reads the batch JSON from the argument, or from stdin when the
// argument is "-".
func readBatchArg(arg string) (batch.Batch, error) {
	raw := []byte(arg)
	if arg == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return batch.Batch{}, fmt.Errorf("read stdin: %w", err)
		}
		raw = data
	}
	return batch.ParseBatch(raw)
}

// runPlan handles `spirit agent plan '<json>'`.
func runPlan() {
	if len(os.Args) < 3 {
		dieUsage(`usage: spirit agent plan '<batch-json>' | spirit agent plan -
batch-json: {"actions":[{"op":"queue","session_id":"...","message":"..."}, ...], "on_error":"stop|continue"} or a bare step array`)
	}
	b, err := readBatchArg(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		os.Exit(1)
	}
	client := connectOrDie()
	defer client.Close()

	plan, err := batch.BuildPlan(daemon.ClientOps{Client: client}, b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		os.Exit(1)
	}
	jsonOut(plan)
}

// runAction handles `spirit agent action '<json>'`. A partial or failed batch
// exits nonzero (fail-fast for shell callers) but still prints the full
// structured result — receipts and the resubmittable remainder.
func runAction() {
	if len(os.Args) < 3 {
		dieUsage(`usage: spirit agent action '<batch-json>' | spirit agent action -
batch-json: {"actions":[...], "on_error":"stop|continue", "resume_of":"bat_..."} or a bare step array`)
	}
	b, err := readBatchArg(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "action: %v\n", err)
		os.Exit(1)
	}
	client := connectOrDie()
	defer client.Close()

	result, err := batch.Execute(daemon.ClientOps{Client: client}, b)
	if err != nil {
		fmt.Fprintf(os.Stderr, "action: %v\n", err)
		os.Exit(1)
	}
	jsonOut(result)
	if result.Outcome != batch.OutcomeCompleted {
		os.Exit(1)
	}
}

// runRunbook handles `spirit agent runbook list|explain|plan|run <name> [--param k=v ...]`.
func runRunbook() {
	if len(os.Args) < 3 {
		dieUsage("usage: spirit agent runbook list | explain <name> | plan <name> [--param k=v ...] | run <name> [--param k=v ...]")
	}
	sub := os.Args[2]

	switch sub {
	case "list":
		jsonOut(runbook.List())
		return
	case "explain":
		if len(os.Args) < 4 {
			dieUsage("usage: spirit agent runbook explain <name>")
		}
		rb, err := runbook.Load(os.Args[3])
		if err != nil {
			fmt.Fprintf(os.Stderr, "runbook explain: %v\n", err)
			os.Exit(1)
		}
		jsonOut(rb)
		return
	case "plan", "run":
		if len(os.Args) < 4 {
			dieUsage("usage: spirit agent runbook " + sub + " <name> [--param k=v ...]")
		}
		name := os.Args[3]
		params, err := parseRunbookParams(os.Args[4:])
		if err != nil {
			dieUsage("runbook " + sub + ": " + err.Error())
		}
		client := connectOrDie()
		defer client.Close()
		ops := daemon.ClientOps{Client: client}

		if sub == "plan" {
			_, plan, err := scripting.RunbookPlan(ops, name, params)
			if err != nil {
				fmt.Fprintf(os.Stderr, "runbook plan: %v\n", err)
				os.Exit(1)
			}
			jsonOut(plan)
			return
		}
		_, result, err := scripting.RunbookRun(ops, name, params)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runbook run: %v\n", err)
			os.Exit(1)
		}
		jsonOut(result)
		if result.Outcome != batch.OutcomeCompleted {
			os.Exit(1)
		}
		return
	default:
		dieUsage("unknown runbook subcommand: " + sub + "\nusage: spirit agent runbook list|explain|plan|run ...")
	}
}

// parseRunbookParams parses repeated --param k=v flags (also accepts k=v
// positionally).
func parseRunbookParams(args []string) (map[string]string, error) {
	params := map[string]string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--param" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("--param requires k=v")
			}
			i++
			arg = args[i]
		}
		k, v, ok := strings.Cut(arg, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid param %q (want k=v)", arg)
		}
		params[k] = v
	}
	return params, nil
}
