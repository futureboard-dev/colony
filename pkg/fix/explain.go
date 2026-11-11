package fix

import (
	"context"
	"regexp"
	"strings"
)

// ruleExplainer is the heuristic Explainer: it matches a block's raw
// reason against a handful of known shapes (missing file/path, failing
// gate, review rejection, max cycles) and otherwise passes the raw
// reason through unchanged as the cause.
type ruleExplainer struct{}

func init() {
	SetExplainer(ruleExplainer{})
}

var (
	missingFileRe  = regexp.MustCompile(`(?i)(no such file or directory|cannot find|file not found|missing file|enoent)`)
	failingGateRe  = regexp.MustCompile(`(?i)(gate (failed|failing)|lint failed|tests? failed|type[ -]?check(ing)? failed|build failed|failing gate)`)
	reviewRejectRe = regexp.MustCompile(`(?i)(review rejected|changes requested|rejected by reviewer|reviewer rejected)`)
	maxCyclesRe    = regexp.MustCompile(`(?i)(max(imum)? (cycles|attempts|retries)( reached| exceeded)?|too many (cycles|attempts|retries)|cycle limit reached)`)
)

// Explain implements Explainer using heuristics over req.Reason.
func (ruleExplainer) Explain(ctx context.Context, req Request) (Explanation, error) {
	reason := strings.TrimSpace(req.Reason)

	switch {
	case missingFileRe.MatchString(reason):
		return Explanation{
			Cause:    "A file or path the task needed could not be found. This usually means the spec points at a wrong or outdated path.",
			NextStep: "Check the path referenced in the error against the spec, then fix the path or create the missing file.",
		}, nil
	case failingGateRe.MatchString(reason):
		return Explanation{
			Cause:    "A quality gate (lint, type check, build, or tests) failed.",
			NextStep: "Open the gate's output to find the failure, fix it, and retry.",
		}, nil
	case reviewRejectRe.MatchString(reason):
		return Explanation{
			Cause:    "A reviewer rejected the change or asked for changes.",
			NextStep: "Read the review notes, make the requested changes, and retry.",
		}, nil
	case maxCyclesRe.MatchString(reason):
		return Explanation{
			Cause:    "The task used up its allowed fix cycles without resolving.",
			NextStep: "Review what was already tried, add a hint or adjust the approach, then retry.",
		}, nil
	default:
		return Explanation{
			Cause:    reason,
			NextStep: "",
		}, nil
	}
}
