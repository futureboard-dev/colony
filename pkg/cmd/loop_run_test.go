package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/futureboard-dev/colony/pkg/config"
	"github.com/futureboard-dev/colony/pkg/storage"
	"github.com/spf13/cobra"
)

// allGatesSkipped is a GateOverrides value that disables every gate step, so
// integration tests exercise git plumbing without needing a real toolchain.
const allGatesSkipped = "format,vet,lint,typecheck,test,build"

// setupEpicRoot creates a bare-ish root repo with a base branch ("main") and
// an epic branch ("feature/<name>") branched from it, both containing one
// commit. Returns the repo root.
func setupEpicRoot(t *testing.T, name string) (root, epicBranch string) {
	t.Helper()
	root = t.TempDir()
	epicBranch = "feature/" + name
	runCmd(t, root, "git", "init", "--initial-branch=main")
	runCmd(t, root, "git", "config", "user.email", "test@example.com")
	runCmd(t, root, "git", "config", "user.name", "test")
	writeFileForCmd(t, root, "base.txt", "base")
	runCmd(t, root, "git", "add", "base.txt")
	runCmd(t, root, "git", "commit", "-m", "base", "--quiet")
	runCmd(t, root, "git", "branch", epicBranch, "main")
	return root, epicBranch
}

// addWorktreeAt creates a git worktree for branch (created via -b) or, if
// branch already exists, checks it out — off startPoint, inside root.
func addWorktreeAt(t *testing.T, root, dir, branch, startPoint string, newBranch bool) string {
	t.Helper()
	if newBranch {
		runCmd(t, root, "git", "worktree", "add", dir, "-b", branch, startPoint, "--quiet")
	} else {
		runCmd(t, root, "git", "worktree", "add", dir, branch, "--quiet")
	}
	return dir
}

func revParseAt(t *testing.T, dir, ref string) string {
	t.Helper()
	return runCmdOut(t, dir, "git", "rev-parse", ref)
}

func TestIntegrateTask_GroupMergesLocalEpic(t *testing.T) {
	root, epicBranch := setupEpicRoot(t, "payments")

	// Simulate an earlier part already landed on the epic branch.
	epicWT := filepath.Join(t.TempDir(), "epic")
	addWorktreeAt(t, root, epicWT, epicBranch, "", false)
	writeFileForCmd(t, epicWT, "epic.txt", "landed part content")
	runCmd(t, epicWT, "git", "add", "epic.txt")
	runCmd(t, epicWT, "git", "commit", "-m", "epic: part one", "--quiet")
	runCmd(t, root, "git", "worktree", "remove", epicWT, "--force")

	// A new part branched from the epic tip, with its own new commit.
	partBranch := "agent/part-two"
	partWT := filepath.Join(t.TempDir(), "part")
	addWorktreeAt(t, root, partWT, partBranch, epicBranch, true)
	writeFileForCmd(t, partWT, "part.txt", "new part content")
	runCmd(t, partWT, "git", "add", "part.txt")
	runCmd(t, partWT, "git", "commit", "-m", "wip: part two", "--quiet")

	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task := storage.Task{
		ID: "t2", Description: "part two", State: "running",
		GroupID: "g1", BaseBranch: epicBranch, GateOverrides: allGatesSkipped,
		CreatedAt: time.Now(),
	}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	wantEpicSHA := revParseAt(t, root, epicBranch)

	gotSHA, err := integrateTask(context.Background(), nil, partWT, partBranch, epicBranch, "go", &task, store, os.Stderr)
	if err != nil {
		t.Fatalf("integrateTask: %v", err)
	}
	if gotSHA != wantEpicSHA {
		t.Errorf("expected returned epicSHA %q, got %q", wantEpicSHA, gotSHA)
	}

	// The epic's content merged in — no fetch was needed (there is no origin
	// remote in this repo at all, so a fetch attempt would have errored).
	if _, err := os.Stat(filepath.Join(partWT, "epic.txt")); err != nil {
		t.Errorf("expected the epic's file to be merged into the part worktree: %v", err)
	}
}

func TestIntegrateTask_NonGroupUnchanged(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init", "--initial-branch=main")
	runCmd(t, root, "git", "config", "user.email", "test@example.com")
	runCmd(t, root, "git", "config", "user.name", "test")
	writeFileForCmd(t, root, "base.txt", "base")
	runCmd(t, root, "git", "add", "base.txt")
	runCmd(t, root, "git", "commit", "-m", "base", "--quiet")
	runCmd(t, root, "git", "remote", "add", "origin", root)
	runCmd(t, root, "git", "push", "origin", "main")

	branch := "agent/standalone"
	wt := filepath.Join(t.TempDir(), "wt")
	addWorktreeAt(t, root, wt, branch, "main", true)
	writeFileForCmd(t, wt, "change.txt", "standalone change")
	runCmd(t, wt, "git", "add", "change.txt")
	runCmd(t, wt, "git", "commit", "-m", "wip: standalone", "--quiet")

	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	task := storage.Task{ID: "t1", Description: "standalone", State: "running", GateOverrides: allGatesSkipped, CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	epicSHA, err := integrateTask(context.Background(), nil, wt, branch, "main", "go", &task, store, os.Stderr)
	if err != nil {
		t.Fatalf("integrateTask: %v", err)
	}
	if epicSHA != "" {
		t.Errorf("expected no epicSHA for a non-group task, got %q", epicSHA)
	}
}

// siblingEditSetup returns a group part's worktree whose base has shared.txt,
// after a sibling part landed a change to its first line on the epic branch.
func siblingEditSetup(t *testing.T) (partWT, epicBranch string, task *storage.Task, store *storage.SQLiteStore) {
	t.Helper()
	root, epicBranch := setupEpicRoot(t, "siblings")
	runCmd(t, root, "git", "config", "core.excludesFile", filepath.Join(root, "no-global-excludes"))
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(".colony/\n"), 0644); err != nil {
		t.Fatal(err)
	}

	epicWT := filepath.Join(t.TempDir(), "epic")
	addWorktreeAt(t, root, epicWT, epicBranch, "", false)
	writeFileForCmd(t, epicWT, "shared.txt", "one\ntwo\nthree\n")
	runCmd(t, epicWT, "git", "add", "shared.txt")
	runCmd(t, epicWT, "git", "commit", "-m", "epic: shared file", "--quiet")

	partWT = filepath.Join(t.TempDir(), "part")
	addWorktreeAt(t, root, partWT, "agent/part", epicBranch, true)

	writeFileForCmd(t, epicWT, "shared.txt", "ONE from sibling\ntwo\nthree\n")
	runCmd(t, epicWT, "git", "commit", "-am", "epic: sibling part", "--quiet")
	runCmd(t, root, "git", "worktree", "remove", epicWT, "--force")

	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	task = &storage.Task{
		ID: "t-part", Description: "part", State: "running",
		GroupID: "g1", BaseBranch: epicBranch, GateOverrides: allGatesSkipped,
		CreatedAt: time.Now(),
	}
	if err := store.InsertTask(*task); err != nil {
		t.Fatal(err)
	}
	return partWT, epicBranch, task, store
}

func taskState(t *testing.T, store *storage.SQLiteStore, id string) storage.Task {
	t.Helper()
	tasks, err := store.QueryTasks(storage.TaskFilter{ID: id})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("query task %s: %v (%d found)", id, err, len(tasks))
	}
	return tasks[0]
}

// The agent leaves its work uncommitted. Integration must commit it before
// merging, or git refuses a merge that touches the same files.
func TestIntegrateTask_UncommittedWorkInSiblingFile(t *testing.T) {
	partWT, epicBranch, task, store := siblingEditSetup(t)
	writeFileForCmd(t, partWT, "shared.txt", "one\ntwo\nTHREE from part\n")
	writeFileForCmd(t, partWT, "SPEC.md", "colony's task spec")

	if _, err := integrateTask(context.Background(), nil, partWT, "agent/part", epicBranch, "go", task, store, io.Discard); err != nil {
		t.Fatalf("integrateTask: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(partWT, "shared.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "ONE from sibling\ntwo\nTHREE from part\n"; got != want {
		t.Errorf("shared.txt = %q, want both parts' changes %q", got, want)
	}
	if tracked := runCmdOut(t, partWT, "git", "ls-files", "SPEC.md"); tracked != "" {
		t.Errorf("SPEC.md was committed")
	}
}

func TestIntegrateTask_MergeConflict(t *testing.T) {
	t.Run("unresolved conflict blocks with the conflicting files", func(t *testing.T) {
		partWT, epicBranch, task, store := siblingEditSetup(t)
		writeFileForCmd(t, partWT, "shared.txt", "ONE from part\ntwo\nthree\n")

		if _, err := integrateTask(context.Background(), nil, partWT, "agent/part", epicBranch, "go", task, store, io.Discard); err == nil {
			t.Fatal("expected a merge conflict error")
		}
		got := taskState(t, store, task.ID)
		if got.State != "blocked" || !strings.Contains(got.LastFeedback, "shared.txt") {
			t.Errorf("task = %q %q, want blocked naming shared.txt", got.State, got.LastFeedback)
		}
		if out := runCmdOut(t, partWT, "git", "status", "--porcelain"); out != "" {
			t.Errorf("worktree left mid-merge: %q", out)
		}
	})

	t.Run("fixer agent resolves the conflict", func(t *testing.T) {
		partWT, epicBranch, task, store := siblingEditSetup(t)
		writeFileForCmd(t, partWT, "shared.txt", "ONE from part\ntwo\nthree\n")
		agent := filepath.Join(t.TempDir(), "agent")
		if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf 'ONE from both\\ntwo\\nthree\\n' > shared.txt\n"), 0755); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{LLM: config.LLMConfig{Provider: "anthropic", CLI: agent}}

		if _, err := integrateTask(context.Background(), cfg, partWT, "agent/part", epicBranch, "go", task, store, io.Discard); err != nil {
			t.Fatalf("integrateTask: %v", err)
		}
		if got := runCmdOut(t, partWT, "git", "show", "HEAD:shared.txt"); got != "ONE from both\ntwo\nthree" {
			t.Errorf("committed shared.txt = %q", got)
		}
		if parents := runCmdOut(t, partWT, "git", "rev-list", "--parents", "-n1", "HEAD"); len(strings.Fields(parents)) != 3 {
			t.Errorf("HEAD is not a merge commit: %q", parents)
		}
		if err := exec.Command("git", "-C", partWT, "merge-base", "--is-ancestor", epicBranch, "HEAD").Run(); err != nil {
			t.Errorf("epic branch not merged into HEAD: %v", err)
		}
	})
}

func TestLandOnEpic(t *testing.T) {
	t.Run("two parts land sequentially with one squashed commit each", func(t *testing.T) {
		root, epicBranch := setupEpicRoot(t, "payments")
		store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		landPart := func(id, branch string, mutations int) {
			epicSHA := revParseAt(t, root, epicBranch)
			wt := filepath.Join(t.TempDir(), id)
			addWorktreeAt(t, root, wt, branch, epicBranch, true)
			for i := range mutations {
				writeFileForCmd(t, wt, id+"-file.txt", strings.Repeat("x", i+1))
				runCmd(t, wt, "git", "add", ".")
				runCmd(t, wt, "git", "commit", "-m", "wip: "+id, "--quiet")
			}
			task := storage.Task{ID: id, Description: id, State: "running", GroupID: "g1", BaseBranch: epicBranch, CreatedAt: time.Now()}
			if err := store.InsertTask(task); err != nil {
				t.Fatal(err)
			}
			if err := landOnEpic(context.Background(), nil, wt, branch, "go", &task, epicSHA, store, os.Stderr); err != nil {
				t.Fatalf("landOnEpic(%s): %v", id, err)
			}
		}

		landPart("part-a", "agent/part-a", 2)
		landPart("part-b", "agent/part-b", 1)

		count := atoi(runCmdOut(t, root, "git", "rev-list", "--count", epicBranch, "^main"))
		if count != 2 {
			t.Errorf("expected 2 squashed commits on the epic branch, got %d", count)
		}

		for _, id := range []string{"part-a", "part-b"} {
			tasks, err := store.QueryTasks(storage.TaskFilter{ID: id})
			if err != nil || len(tasks) != 1 {
				t.Fatalf("query %s: %v", id, err)
			}
			if tasks[0].State != "done" {
				t.Errorf("expected %s to be done, got %q", id, tasks[0].State)
			}
			if tasks[0].LastFeedback != epicBranch {
				t.Errorf("expected %s feedback to name the epic branch, got %q", id, tasks[0].LastFeedback)
			}
		}
	})

	t.Run("notes move to the spec folder and spec files never land", func(t *testing.T) {
		root, epicBranch := setupEpicRoot(t, "payments")
		store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		specDir := t.TempDir()
		partPath := filepath.Join(specDir, "parts", "01-api.md")
		if err := os.MkdirAll(filepath.Dir(partPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(partPath, []byte(formatPartFile(specPart{ID: "01-api", Body: "x"})), 0644); err != nil {
			t.Fatal(err)
		}

		epicSHA := revParseAt(t, root, epicBranch)
		branch := "agent/01-api"
		wt := filepath.Join(t.TempDir(), "01-api")
		addWorktreeAt(t, root, wt, branch, epicBranch, true)
		if err := excludeFromGit(wt, "/"+partNotesFile); err != nil {
			t.Fatal(err)
		}
		writeFileForCmd(t, wt, "SPEC.md", "spec")
		writeFileForCmd(t, wt, partNotesFile, "built ApiHandler")
		writeFileForCmd(t, wt, "api.txt", "code")
		// An agent that commits everything mid-run must still not leak either file.
		runCmd(t, wt, "git", "add", "-A")
		runCmd(t, wt, "git", "commit", "-m", "wip", "--quiet")

		task := storage.Task{ID: "t1", Description: "api", State: "running", GroupID: "g1", BaseBranch: epicBranch, SpecPath: partPath, CreatedAt: time.Now()}
		if err := store.InsertTask(task); err != nil {
			t.Fatal(err)
		}
		if err := landOnEpic(context.Background(), nil, wt, branch, "go", &task, epicSHA, store, os.Stderr); err != nil {
			t.Fatalf("landOnEpic: %v", err)
		}

		tree := runCmdOut(t, root, "git", "ls-tree", "--name-only", epicBranch)
		if !strings.Contains(tree, "api.txt") || strings.Contains(tree, "SPEC.md") || strings.Contains(tree, partNotesFile) {
			t.Errorf("epic tree should hold only the code, got:\n%s", tree)
		}
		notes, err := os.ReadFile(filepath.Join(specDir, "notes", "01-api.md"))
		if err != nil || string(notes) != "built ApiHandler" {
			t.Errorf("expected notes in the spec folder, got %q err=%v", notes, err)
		}
		if _, err := os.Stat(filepath.Join(wt, partNotesFile)); !os.IsNotExist(err) {
			t.Error("expected the worktree notes file to be removed")
		}
	})

	t.Run("stale epic sha triggers remerge and retry", func(t *testing.T) {
		root, epicBranch := setupEpicRoot(t, "payments")
		store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		staleSHA := revParseAt(t, root, epicBranch)

		branch := "agent/part-c"
		wt := filepath.Join(t.TempDir(), "part-c")
		addWorktreeAt(t, root, wt, branch, epicBranch, true)
		writeFileForCmd(t, wt, "part-c.txt", "content")
		runCmd(t, wt, "git", "add", ".")
		runCmd(t, wt, "git", "commit", "-m", "wip: part-c", "--quiet")

		// Simulate a sibling landing in between: move the epic branch forward
		// via another worktree, so staleSHA no longer matches the live ref.
		siblingWT := filepath.Join(t.TempDir(), "sibling")
		addWorktreeAt(t, root, siblingWT, epicBranch, "", false)
		writeFileForCmd(t, siblingWT, "sibling.txt", "sibling content")
		runCmd(t, siblingWT, "git", "add", ".")
		runCmd(t, siblingWT, "git", "commit", "-m", "epic: sibling landed", "--quiet")
		runCmd(t, root, "git", "worktree", "remove", siblingWT, "--force")

		task := storage.Task{
			ID: "part-c", Description: "part-c", State: "running", GroupID: "g1",
			BaseBranch: epicBranch, GateOverrides: allGatesSkipped, CreatedAt: time.Now(),
		}
		if err := store.InsertTask(task); err != nil {
			t.Fatal(err)
		}

		if err := landOnEpic(context.Background(), nil, wt, branch, "go", &task, staleSHA, store, os.Stderr); err != nil {
			t.Fatalf("landOnEpic: %v", err)
		}

		tasks, err := store.QueryTasks(storage.TaskFilter{ID: "part-c"})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("query: %v", err)
		}
		if tasks[0].State != "done" {
			t.Errorf("expected part-c to be done after retrying past the stale ref, got %q", tasks[0].State)
		}

		// Both the sibling's commit and our squashed commit are on the epic branch.
		count := atoi(runCmdOut(t, root, "git", "rev-list", "--count", epicBranch, "^main"))
		if count != 2 {
			t.Errorf("expected 2 commits on the epic branch (sibling + our squash), got %d", count)
		}
	})

	t.Run("three CAS failures mark blocked", func(t *testing.T) {
		root, epicBranch := setupEpicRoot(t, "payments")
		store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		staleSHA := revParseAt(t, root, epicBranch)

		branch := "agent/part-d"
		wt := filepath.Join(t.TempDir(), "part-d")
		addWorktreeAt(t, root, wt, branch, epicBranch, true)
		writeFileForCmd(t, wt, "part-d.txt", "content")
		runCmd(t, wt, "git", "add", ".")
		runCmd(t, wt, "git", "commit", "-m", "wip: part-d", "--quiet")

		// Prepare a chain of dummy commits reachable from the epic tip, and a
		// background goroutine that keeps force-advancing the epic ref through
		// them while landOnEpic runs — so every one of its 3 attempts observes
		// a ref that has already moved again by the time it tries to CAS.
		churnWT := filepath.Join(t.TempDir(), "churn")
		addWorktreeAt(t, root, churnWT, "agent/churn", epicBranch, true)
		var dummySHAs []string
		for i := range 20 {
			writeFileForCmd(t, churnWT, "churn.txt", strings.Repeat("x", i+1))
			runCmd(t, churnWT, "git", "add", ".")
			runCmd(t, churnWT, "git", "commit", "-m", "churn", "--quiet")
			dummySHAs = append(dummySHAs, revParseAt(t, churnWT, "HEAD"))
		}
		runCmd(t, root, "git", "worktree", "remove", churnWT, "--force")

		stop := make(chan struct{})
		var wg sync.WaitGroup
		var idx atomic.Int64
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				i := idx.Add(1)
				if int(i) >= len(dummySHAs) {
					return
				}
				_ = exec.Command("git", "-C", root, "update-ref", "refs/heads/"+epicBranch, dummySHAs[i]).Run()
				time.Sleep(time.Millisecond)
			}
		})

		task := storage.Task{
			ID: "part-d", Description: "part-d", State: "running", GroupID: "g1",
			BaseBranch: epicBranch, GateOverrides: allGatesSkipped, CreatedAt: time.Now(),
		}
		if err := store.InsertTask(task); err != nil {
			t.Fatal(err)
		}

		err = landOnEpic(context.Background(), nil, wt, branch, "go", &task, staleSHA, store, os.Stderr)
		close(stop)
		wg.Wait()

		if err == nil {
			t.Fatal("expected landOnEpic to give up and return an error")
		}
		tasks, qerr := store.QueryTasks(storage.TaskFilter{ID: "part-d"})
		if qerr != nil || len(tasks) != 1 {
			t.Fatalf("query: %v", qerr)
		}
		if tasks[0].State != "blocked" {
			t.Errorf("expected part-d to be blocked after 3 failed CAS attempts, got %q", tasks[0].State)
		}
	})
}

func TestGroupCompletion(t *testing.T) {
	setup := func(t *testing.T) (root, epicBranch, groupID string, store *storage.SQLiteStore) {
		t.Helper()
		root, epicBranch = setupEpicRoot(t, "payments")
		runCmd(t, root, "git", "remote", "add", "origin", root)
		runCmd(t, root, "git", "push", "origin", "main")

		store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
		if err != nil {
			t.Fatal(err)
		}
		groupID = "g1"
		if err := store.InsertGroup(storage.TaskGroup{
			ID: groupID, Name: "payments", EpicBranch: epicBranch, BaseBranch: "main",
			Lang: "go", State: "open", CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"p1", "p2"} {
			if err := store.InsertTask(storage.Task{
				ID: id, Description: id, State: "done", GroupID: groupID, BaseBranch: epicBranch, CreatedAt: time.Now(),
			}); err != nil {
				t.Fatal(err)
			}
		}
		return root, epicBranch, groupID, store
	}

	t.Run("last part pushes epic and opens one PR", func(t *testing.T) {
		root, epicBranch, groupID, store := setup(t)
		defer store.Close()

		err := maybeCompleteGroup(groupID, root, "", store, os.Stderr)

		groups, qerr := store.QueryGroups(storage.GroupFilter{ID: groupID})
		if qerr != nil || len(groups) != 1 {
			t.Fatalf("query group: %v", qerr)
		}
		g := groups[0]

		out := runCmdOut(t, root, "git", "ls-remote", "origin", "refs/heads/"+epicBranch)
		if out == "" {
			t.Fatalf("expected epic branch %s to be pushed to origin", epicBranch)
		}

		if err != nil {
			// `gh pr create` may fail for lack of `gh auth login` in this
			// environment — the same tolerance loop_run_test.go already
			// applies to finishTask's identical push/PR step.
			if !strings.Contains(err.Error(), "gh pr create") {
				t.Fatalf("unexpected maybeCompleteGroup error: %v", err)
			}
			if g.State != "blocked" {
				t.Errorf("expected group blocked when PR creation fails, got %q", g.State)
			}
			return
		}
		if g.State != "done" || g.PRURL == "" {
			t.Errorf("expected group done with pr_url set, got %+v", g)
		}
	})

	t.Run("group marked done with pr_url", func(t *testing.T) {
		if exec.Command("gh", "auth", "status").Run() != nil {
			t.Skip("gh is not authenticated in this environment; the push/PR path can't be exercised end to end")
		}
		root, _, groupID, store := setup(t)
		defer store.Close()

		if err := maybeCompleteGroup(groupID, root, "", store, os.Stderr); err != nil {
			t.Fatalf("maybeCompleteGroup: %v", err)
		}
		groups, err := store.QueryGroups(storage.GroupFilter{ID: groupID})
		if err != nil || len(groups) != 1 {
			t.Fatalf("query group: %v", err)
		}
		if groups[0].State != "done" || groups[0].PRURL == "" {
			t.Errorf("expected group done with pr_url set, got %+v", groups[0])
		}
	})

	t.Run("push failure marks group blocked", func(t *testing.T) {
		root, _, groupID, store := setup(t)
		defer store.Close()
		runCmd(t, root, "git", "remote", "remove", "origin")

		if err := maybeCompleteGroup(groupID, root, "", store, os.Stderr); err == nil {
			t.Fatal("expected an error when push fails")
		}
		groups, err := store.QueryGroups(storage.GroupFilter{ID: groupID})
		if err != nil || len(groups) != 1 {
			t.Fatalf("query group: %v", err)
		}
		if groups[0].State != "blocked" {
			t.Errorf("expected group blocked after push failure, got %q", groups[0].State)
		}
	})
}

// TestFinishTask_NonGroupStillPushesAndPRs pins that finishTask's behavior is
// unchanged now that its push/PR step is the shared pushBranchAndOpenPR
// helper — also used by group completion.
func TestFinishTask_NonGroupStillPushesAndPRs(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init", "--initial-branch=main")
	runCmd(t, root, "git", "config", "user.email", "test@example.com")
	runCmd(t, root, "git", "config", "user.name", "test")
	writeFileForCmd(t, root, "base.txt", "base")
	runCmd(t, root, "git", "add", "base.txt")
	runCmd(t, root, "git", "commit", "-m", "base", "--quiet")
	runCmd(t, root, "git", "remote", "add", "origin", root)
	runCmd(t, root, "git", "push", "origin", "main")

	branch := "feature/standalone"
	runCmd(t, root, "git", "checkout", "-b", branch, "--quiet")
	writeFileForCmd(t, root, "change.txt", "standalone change")
	runCmd(t, root, "git", "add", "change.txt")
	runCmd(t, root, "git", "commit", "-m", "wip: standalone", "--quiet")

	prURL, err := finishTask(root, branch, "main", "standalone-label", "", os.Stderr)
	if err != nil && !strings.Contains(err.Error(), "gh pr create") {
		t.Fatalf("finishTask: %v", err)
	}
	_ = prURL

	out := runCmdOut(t, root, "git", "ls-remote", "origin", "refs/heads/"+branch)
	if out == "" {
		t.Fatalf("expected branch %s to be pushed to origin via the shared push/PR helper", branch)
	}
}

func TestLoopRun_LangValidation(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	cases := []struct {
		name    string
		lang    string
		wantErr string
	}{
		// An omitted --lang is only rejected once the task is known to have no
		// recorded language, so this case fails on lookup, not on the flag.
		{"missing lang", "", "not found"},
		{"invalid lang", "rust", "unknown language"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loopRunLang = tc.lang
			err := runLoopRun(&cobra.Command{}, []string{"some-id"})
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// A task carrying no language cannot be gated without --lang. (The inverse —
// a task with a recorded language running flagless — is not asserted here
// because it proceeds into a real build.)
func TestLoopRun_MissingLangOnlyFailsForUnsetTask(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	store, err := storage.Open(filepath.Join(dir, ".colony", "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.InsertTask(storage.Task{ID: "no-lang", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	loopRunLang = ""
	err = runLoopRun(&cobra.Command{}, []string{"no-lang"})
	if err == nil || !strings.Contains(err.Error(), "no recorded language") {
		t.Fatalf("expected a missing-language error, got %v", err)
	}
}

func TestLoopRun_TaskNotFound(t *testing.T) {
	dir := initTestRepo(t)
	setupMinimalProject(t, dir)

	origWd, _ := os.Getwd()
	_ = os.Chdir(dir)
	defer func() { _ = os.Chdir(origWd) }()

	loopRunLang = "go"
	err := runLoopRun(&cobra.Command{}, []string{"missing-id"})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got %v", err)
	}
}

func TestUpdateTaskLang(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(dir + "/missions.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.InsertTask(storage.Task{
		ID: "t1", Description: "d", State: "blocked", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.UpdateTaskLang("t1", "typescript"); err != nil {
		t.Fatal(err)
	}

	tasks, err := store.QueryTasks(storage.TaskFilter{ID: "t1"})
	if err != nil || len(tasks) != 1 {
		t.Fatalf("query: %v tasks=%d", err, len(tasks))
	}
	if tasks[0].Lang != "typescript" {
		t.Errorf("expected lang typescript, got %q", tasks[0].Lang)
	}
}

func TestResolveGateLang(t *testing.T) {
	newStore := func(t *testing.T, task storage.Task) *storage.SQLiteStore {
		t.Helper()
		store, err := storage.Open(t.TempDir() + "/missions.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task.CreatedAt = time.Now()
		if err := store.InsertTask(task); err != nil {
			t.Fatal(err)
		}
		return store
	}

	t.Run("recorded lang wins over the flag", func(t *testing.T) {
		task := storage.Task{ID: "t1", Description: "d", State: "open", Lang: "typescript"}
		got, err := resolveGateLang(newStore(t, task), &task, "go", true)
		if err != nil || got != "typescript" {
			t.Errorf("expected typescript, got %q err=%v", got, err)
		}
	})

	t.Run("legacy task without an explicit flag is refused", func(t *testing.T) {
		task := storage.Task{ID: "t2", Description: "d", State: "open"}
		_, err := resolveGateLang(newStore(t, task), &task, "go", false)
		if err == nil {
			t.Fatal("expected a refusal for a task with no recorded language")
		}
		if !strings.Contains(err.Error(), "no recorded language") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("explicit flag fills in and persists", func(t *testing.T) {
		task := storage.Task{ID: "t3", Description: "d", State: "open"}
		store := newStore(t, task)
		got, err := resolveGateLang(store, &task, "typescript", true)
		if err != nil || got != "typescript" {
			t.Fatalf("expected typescript, got %q err=%v", got, err)
		}
		tasks, err := store.QueryTasks(storage.TaskFilter{ID: "t3"})
		if err != nil || len(tasks) != 1 {
			t.Fatalf("query: %v tasks=%d", err, len(tasks))
		}
		if tasks[0].Lang != "typescript" {
			t.Errorf("expected the language to be persisted, got %q", tasks[0].Lang)
		}
	})

	t.Run("explicit flag is validated", func(t *testing.T) {
		task := storage.Task{ID: "t4", Description: "d", State: "open"}
		if _, err := resolveGateLang(newStore(t, task), &task, "rust", true); err == nil {
			t.Error("expected an error for an unsupported language")
		}
	})
}

// TestProcessTask_RefusesUnsetLang pins the refusal at the loop's entry point:
// the language is resolved before any worktree or runner work happens, so a
// legacy task is never built with the wrong toolchain.
func TestProcessTask_RefusesUnsetLang(t *testing.T) {
	store, err := storage.Open(t.TempDir() + "/missions.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	task := storage.Task{ID: "legacy-1", Description: "d", State: "open", CreatedAt: time.Now()}
	if err := store.InsertTask(task); err != nil {
		t.Fatal(err)
	}

	origLang, origExplicit := loopLang, loopLangExplicit
	defer func() { loopLang, loopLangExplicit = origLang, origExplicit }()
	loopLang, loopLangExplicit = "go", false

	err = processTask(context.Background(), nil, t.TempDir(), store, &task, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "no recorded language") {
		t.Fatalf("expected a refusal for a task with no recorded language, got %v", err)
	}
}

func TestRunLoop_ResetsRunningOnStart(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.InsertTask(storage.Task{ID: "r1", Description: "d", State: "running", LastFeedback: "fb", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{ID: "o1", Description: "d", State: "open", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if err := resetRunningTasks(store); err != nil {
		t.Fatalf("resetRunningTasks: %v", err)
	}

	tasks, err := store.QueryTasks(storage.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]storage.Task{}
	for _, tk := range tasks {
		byID[tk.ID] = tk
	}
	if byID["r1"].State != "needs-fix" {
		t.Errorf("expected r1 to become needs-fix, got %q", byID["r1"].State)
	}
	if byID["r1"].LastFeedback != "fb" {
		t.Errorf("expected feedback to be preserved, got %q", byID["r1"].LastFeedback)
	}
	if byID["o1"].State != "open" {
		t.Errorf("expected o1 (not running) to stay open, got %q", byID["o1"].State)
	}
}

func TestRunLoop_ParallelDefaultOneUnchanged(t *testing.T) {
	cases := []struct{ in, want int }{{0, 1}, {1, 1}, {4, 4}}
	for _, tc := range cases {
		if got := resolveParallelCount(tc.in); got != tc.want {
			t.Errorf("resolveParallelCount(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestRunLoop_ParallelRespectsN(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const n = 6
	for i := range n {
		if err := store.InsertTask(storage.Task{
			ID: fmt.Sprintf("p%d", i), Description: "d", State: "open",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
	}

	var active, peak atomic.Int64
	work := func(_ context.Context, tsk *storage.Task, _ io.Writer) error {
		cur := active.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		active.Add(-1)
		return store.UpdateTaskState(tsk.ID, "done", "")
	}

	// maxPasses == n: the loop stops right after claiming the last task, so
	// it never has to wait through an idle sleep.
	if err := runLoopParallel(context.Background(), t.TempDir(), store, n, 100, 2, work); err != nil {
		t.Fatalf("runLoopParallel: %v", err)
	}
	if got := peak.Load(); got > 2 {
		t.Errorf("expected peak concurrency <= 2, got %d", got)
	}
	if got := peak.Load(); got < 2 {
		t.Errorf("expected peak concurrency to actually reach 2, got %d", got)
	}
}

func TestRunLoop_ParallelRespectsDeps(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.InsertTask(storage.Task{ID: "d01", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{ID: "d02", Description: "d", State: "open", DependsOn: "d01", CreatedAt: now.Add(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{ID: "d03", Description: "d", State: "open", DependsOn: "d01", CreatedAt: now.Add(2 * time.Millisecond)}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	starts := map[string]time.Time{}

	work := func(_ context.Context, tsk *storage.Task, _ io.Writer) error {
		mu.Lock()
		starts[tsk.ID] = time.Now()
		mu.Unlock()
		if tsk.ID == "d01" {
			time.Sleep(30 * time.Millisecond)
		}
		return store.UpdateTaskState(tsk.ID, "done", "")
	}

	if err := runLoopParallel(context.Background(), t.TempDir(), store, 3, 100, 2, work); err != nil {
		t.Fatalf("runLoopParallel: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(starts) != 3 {
		t.Fatalf("expected all 3 tasks to run, got %d", len(starts))
	}
	if !starts["d02"].After(starts["d01"]) || !starts["d03"].After(starts["d01"]) {
		t.Errorf("expected d02 and d03 to start only after d01, got starts=%v", starts)
	}
}

func TestRunLoop_ParallelNotIdleWhileDependencyRuns(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now()
	if err := store.InsertTask(storage.Task{ID: "i01", Description: "d", State: "open", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTask(storage.Task{ID: "i02", Description: "d", State: "open", DependsOn: "i01", CreatedAt: now.Add(time.Millisecond)}); err != nil {
		t.Fatal(err)
	}

	work := func(_ context.Context, tsk *storage.Task, _ io.Writer) error {
		if tsk.ID == "i01" {
			time.Sleep(200 * time.Millisecond)
		}
		return store.UpdateTaskState(tsk.ID, "done", "")
	}

	// idleLimit=1: before the fix, the first empty pick while i01 was still
	// running ended the loop and i02 never started.
	if err := runLoopParallel(context.Background(), t.TempDir(), store, 2, 1, 2, work); err != nil {
		t.Fatalf("runLoopParallel: %v", err)
	}
	tasks, err := store.QueryTasks(storage.TaskFilter{ID: "i02"})
	if err != nil || len(tasks) != 1 || tasks[0].State != "done" {
		t.Fatalf("expected i02 to run once i01 finished, got %+v err=%v", tasks, err)
	}
}

func TestExcludeFromGit(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init", "--initial-branch=main", "--quiet")
	writeFileForCmd(t, root, partNotesFile, "notes")

	for range 2 {
		if err := excludeFromGit(root, "/"+partNotesFile); err != nil {
			t.Fatal(err)
		}
	}
	if status := runCmdOut(t, root, "git", "status", "--porcelain"); strings.Contains(status, partNotesFile) {
		t.Errorf("expected %s to be ignored, status:\n%s", partNotesFile, status)
	}
	data, err := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), "/"+partNotesFile); n != 1 {
		t.Errorf("expected the pattern once, found %d times", n)
	}
}

func TestRunLoop_ParallelWaitsForInFlightOnStop(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "missions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.InsertTask(storage.Task{ID: "w1", Description: "d", State: "open", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	var finished atomic.Bool
	work := func(_ context.Context, tsk *storage.Task, _ io.Writer) error {
		time.Sleep(100 * time.Millisecond)
		finished.Store(true)
		return store.UpdateTaskState(tsk.ID, "done", "")
	}

	// maxPasses=1: the main loop claims the one task and immediately hits
	// max-passes, while the worker is still sleeping.
	if err := runLoopParallel(context.Background(), t.TempDir(), store, 1, 100, 2, work); err != nil {
		t.Fatalf("runLoopParallel: %v", err)
	}
	if !finished.Load() {
		t.Error("expected runLoopParallel to wait for the in-flight worker before returning")
	}
}

func TestResolveWorktree_ConcurrentSafe(t *testing.T) {
	root := t.TempDir()
	runCmd(t, root, "git", "init", "--initial-branch=main")
	runCmd(t, root, "git", "config", "user.email", "test@example.com")
	runCmd(t, root, "git", "config", "user.name", "test")
	writeFileForCmd(t, root, "base.txt", "base")
	runCmd(t, root, "git", "add", "base.txt")
	runCmd(t, root, "git", "commit", "-m", "base", "--quiet")

	wtBase := t.TempDir()
	origBase := os.Getenv("COLONY_WORKTREE_BASE")
	os.Setenv("COLONY_WORKTREE_BASE", wtBase)
	defer os.Setenv("COLONY_WORKTREE_BASE", origBase)

	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	paths := make(chan string, n)
	for i := range n {
		wg.Go(func() {
			task := &storage.Task{Description: fmt.Sprintf("concurrent task %d", i), BaseBranch: "main"}
			wd, _, err := resolveWorktreeSafe(root, "proj", task)
			if err != nil {
				errs <- err
				return
			}
			paths <- wd
		})
	}
	wg.Wait()
	close(errs)
	close(paths)

	for err := range errs {
		t.Fatalf("resolveWorktreeSafe: %v", err)
	}
	seen := map[string]bool{}
	for p := range paths {
		if seen[p] {
			t.Errorf("duplicate worktree path %q", p)
		}
		seen[p] = true
	}
	if len(seen) != n {
		t.Errorf("expected %d distinct worktrees, got %d", n, len(seen))
	}
}
