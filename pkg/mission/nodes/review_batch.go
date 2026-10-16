package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/futureboard-dev/colony/pkg/handoff"
	"github.com/futureboard-dev/colony/pkg/mission/graph"
	"github.com/futureboard-dev/colony/pkg/prompt"
)

// batchFeedbackLimit bounds each batch's share of the combined fixer feedback,
// so a many-batch rejection still fits handoff.FeedbackLimit.
const batchFeedbackLimit = 1536

// minBatchCapacity is the smallest diff slice worth a model call; below it the
// spec alone nearly fills the budget and the task must be split instead.
const minBatchCapacity = 32 * 1024

// reviewUnit is one labelled slice of the diff: a whole file, or one part of a
// file too large for a single batch.
type reviewUnit struct{ label, text string }

// batchVerdict is the envelope a batch reviewer must return. Covered must
// list exactly the unit labels it was given, or the review fails closed.
type batchVerdict struct {
	Decision graph.Decision `json:"decision"`
	Feedback string         `json:"feedback"`
	Covered  []string       `json:"covered"`
	Notes    string         `json:"notes"`
}

// runBatches reviews a diff too large for one prompt. Every byte lands in
// exactly one batch; each batch must confirm the units it covered; any batch
// rejection rejects the task, and only all-approved batches reach a synthesis
// call that judges cross-file and spec-wide concerns.
func (n *ReviewNode) runBatches(ctx context.Context, workdir, reviewContext, diff, evidencePath string) (graph.Output, error) {
	fail := func(format string, args ...any) (graph.Output, error) {
		return graph.Output{AgentID: n.agentID}, fmt.Errorf("review coverage blocked: "+format+"; nothing was approved; full input: %s", append(args, evidencePath)...)
	}
	// The manifest depends on the batch layout, so size it from a first pass
	// assuming capacity without it, then re-pack with the real overhead.
	overhead := len(prompt.ReviewBatch()) + len(reviewContext) + 4096
	capacity := handoff.ReviewLimit - overhead
	if capacity < minBatchCapacity {
		return fail("spec and context leave %d bytes per batch (minimum %d)", capacity, minBatchCapacity)
	}
	batches := packUnits(splitUnits(diff, capacity), capacity)
	manifest := reviewManifest(batches)
	capacity -= len(manifest)
	if capacity < minBatchCapacity {
		return fail("manifest leaves %d bytes per batch (minimum %d)", capacity, minBatchCapacity)
	}
	batches = packUnits(splitUnits(diff, capacity), capacity)
	manifest = reviewManifest(batches)

	var rejected, verdicts []string
	for i, batch := range batches {
		labels := unitLabels(batch)
		input := fmt.Sprintf("%s\n\n## Review manifest (all batches)\n\n%s\n\n## Batch %d of %d\n\nUnits in this batch:\n- %s\n\n%s",
			reviewContext, manifest, i+1, len(batches), strings.Join(labels, "\n- "), unitText(batch))
		combined := injectInput(prompt.ReviewBatch(), input)
		if len(combined) > handoff.ReviewLimit {
			return fail("batch %d is %d bytes (budget %d)", i+1, len(combined), handoff.ReviewLimit)
		}
		result, err := n.run(ctx, workdir, combined)
		if err != nil {
			return graph.Output{AgentID: n.agentID}, fmt.Errorf("agent %q: review batch %d/%d failed: %w", n.agentID, i+1, len(batches), err)
		}
		var v batchVerdict
		if err := json.Unmarshal([]byte(extractJSON(result.Text)), &v); err != nil {
			return fail("batch %d returned an invalid envelope (%v); log: %s", i+1, err, result.LogPath)
		}
		if v.Decision != graph.APPROVED && v.Decision != graph.REJECTED {
			return fail("batch %d returned decision %q; log: %s", i+1, v.Decision, result.LogPath)
		}
		if missing, extra := coverageGap(labels, v.Covered); len(missing)+len(extra) > 0 {
			return fail("batch %d coverage mismatch (not covered: %v; unexpected: %v); log: %s", i+1, missing, extra, result.LogPath)
		}
		verdicts = append(verdicts, fmt.Sprintf("### Batch %d/%d: %s\n\n%s\n\nCross-file notes: %s",
			i+1, len(batches), v.Decision, handoff.Bound(v.Feedback, batchFeedbackLimit), handoff.Bound(v.Notes, batchFeedbackLimit)))
		if v.Decision == graph.REJECTED {
			rejected = append(rejected, fmt.Sprintf("Batch %d/%d (%s): %s", i+1, len(batches), strings.Join(labels, ", "), handoff.Bound(v.Feedback, batchFeedbackLimit)))
		}
	}
	logPath, err := handoff.Save(workdir, "review-batches", strings.Join(verdicts, "\n\n"))
	if err != nil {
		return graph.Output{}, err
	}
	if len(rejected) > 0 {
		feedback := handoff.Summary(strings.Join(rejected, "\n\n"), logPath, handoff.FeedbackLimit)
		return graph.Output{AgentID: n.agentID, Envelope: graph.Envelope{Decision: graph.REJECTED, Feedback: feedback, Output: mustMarshal("")}}, nil
	}

	synth := injectInput(prompt.ReviewSynthesize(), fmt.Sprintf("%s\n\n## Review manifest\n\n%s\n\n## Batch verdicts\n\n%s",
		reviewContext, manifest, strings.Join(verdicts, "\n\n")))
	if len(synth) > handoff.ReviewLimit {
		return fail("synthesis input is %d bytes (budget %d)", len(synth), handoff.ReviewLimit)
	}
	result, err := n.run(ctx, workdir, synth)
	if err != nil {
		return graph.Output{AgentID: n.agentID}, fmt.Errorf("agent %q: review synthesis failed: %w", n.agentID, err)
	}
	env, err := parseDecision(n.agentID, result)
	if err != nil {
		return graph.Output{AgentID: n.agentID, Raw: result.Text}, err
	}
	env.Feedback = handoff.Summary(env.Feedback, result.LogPath, handoff.FeedbackLimit)
	return graph.Output{AgentID: n.agentID, Envelope: env, Raw: result.Text}, nil
}

// splitUnits cuts a diff into per-file units, splitting any file larger than
// capacity into numbered parts on line boundaries (or mid-line for a single
// oversized line). Concatenating the units' texts reproduces diff exactly.
func splitUnits(diff string, capacity int) []reviewUnit {
	var units []reviewUnit
	for _, file := range splitFiles(diff) {
		label := diffPath(file)
		if len(file) <= capacity {
			units = append(units, reviewUnit{label: label, text: file})
			continue
		}
		var parts []string
		for len(file) > 0 {
			end := min(capacity, len(file))
			if end < len(file) {
				if nl := strings.LastIndexByte(file[:end], '\n'); nl > 0 {
					end = nl + 1
				}
			}
			parts = append(parts, file[:end])
			file = file[end:]
		}
		for i, part := range parts {
			units = append(units, reviewUnit{label: fmt.Sprintf("%s (part %d/%d)", label, i+1, len(parts)), text: part})
		}
	}
	return units
}

// splitFiles splits a git diff at each "diff --git" header.
func splitFiles(diff string) []string {
	var files []string
	for len(diff) > 0 {
		next := strings.Index(diff[1:], "\ndiff --git ")
		if next < 0 {
			files = append(files, diff)
			break
		}
		files = append(files, diff[:next+2])
		diff = diff[next+2:]
	}
	return files
}

// diffPath returns the post-image path from a "diff --git a/x b/y" header.
func diffPath(file string) string {
	header, _, _ := strings.Cut(file, "\n")
	if i := strings.LastIndex(header, " b/"); i >= 0 {
		return header[i+3:]
	}
	return strings.TrimPrefix(header, "diff --git ")
}

// packUnits groups units in order into batches of at most capacity bytes.
func packUnits(units []reviewUnit, capacity int) [][]reviewUnit {
	var batches [][]reviewUnit
	size := 0
	for _, u := range units {
		if len(batches) == 0 || size+len(u.text) > capacity {
			batches = append(batches, nil)
			size = 0
		}
		batches[len(batches)-1] = append(batches[len(batches)-1], u)
		size += len(u.text)
	}
	return batches
}

// reviewManifest lists every unit with its size and batch, giving each batch
// reviewer the shape of the whole change for cross-file judgement.
func reviewManifest(batches [][]reviewUnit) string {
	var b strings.Builder
	for i, batch := range batches {
		for _, u := range batch {
			fmt.Fprintf(&b, "- %s — %d bytes — batch %d/%d\n", u.label, len(u.text), i+1, len(batches))
		}
	}
	return b.String()
}

func unitLabels(batch []reviewUnit) []string {
	labels := make([]string, len(batch))
	for i, u := range batch {
		labels[i] = u.label
	}
	return labels
}

func unitText(batch []reviewUnit) string {
	var b strings.Builder
	for _, u := range batch {
		b.WriteString(u.text)
	}
	return b.String()
}

// coverageGap compares assigned unit labels with those a reviewer reported.
func coverageGap(assigned, covered []string) (missing, extra []string) {
	for _, label := range assigned {
		if !slices.Contains(covered, label) {
			missing = append(missing, label)
		}
	}
	for _, label := range covered {
		if !slices.Contains(assigned, label) {
			extra = append(extra, label)
		}
	}
	return missing, extra
}
