package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"cwatch/internal/hooks"
	"cwatch/internal/process"
)

type fakeProcs struct {
	mu sync.Mutex
	m  map[int]process.Proc
}

func (f *fakeProcs) Lookup(pid int) (process.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[pid]
	if !ok {
		return process.Proc{}, process.ErrNotFound
	}
	return p, nil
}

func openTest(t *testing.T) *Store {
	t.Helper()
	// A path with a space and an apostrophe.
	dir := filepath.Join(t.TempDir(), "state dir's")
	st, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func obs(pid int, start int64, tty, iterm string) Observation {
	return Observation{OwnerPID: pid, OwnerStart: start, OwnerMethod: process.MethodNameAncestor,
		TTY: tty, TerminalKind: TerminalITerm, ITermSessionID: iterm}
}

func record(t *testing.T, st *Store, o Observation, e hooks.Event, at time.Time) Instance {
	t.Helper()
	in, err := st.Record(context.Background(), o, e, true, at)
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func withSession(e hooks.Event, sid string) hooks.Event {
	e.SessionID = sid
	e.Cwd = "/work/repo"
	return e
}

// Acceptance test 1.
func TestThreeSessionsSameDirectory(t *testing.T) {
	st := openTest(t)
	ids := map[string]bool{}
	for i := 1; i <= 3; i++ {
		o := obs(100+i, int64(1000+i), fmt.Sprintf("/dev/ttys00%d", i), "")
		in := record(t, st, o, withSession(ev("SessionStart"), fmt.Sprintf("sess-%d", i)), t0)
		ids[in.InstanceID] = true
	}
	// Only the second session works.
	record(t, st, obs(102, 1002, "/dev/ttys002", ""), withSession(ev("UserPromptSubmit"), "sess-2"), t0.Add(time.Second))
	list, err := st.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || len(ids) != 3 {
		t.Fatalf("got %d rows and %d IDs, want 3", len(list), len(ids))
	}
	for _, in := range list {
		want := Idle
		if in.SessionID == "sess-2" {
			want = Working
		}
		if in.State != want {
			t.Errorf("%s: state %q, want %q", in.SessionID, in.State, want)
		}
		if in.Project != "repo" {
			t.Errorf("project %q", in.Project)
		}
	}
}

// Acceptance test 2.
func TestSameSessionTwoTTYs(t *testing.T) {
	st := openTest(t)
	a := record(t, st, obs(201, 1, "/dev/ttys010", "AAAAAAAA-0000-0000-0000-000000000001"), withSession(ev("SessionStart"), "shared"), t0)
	b := record(t, st, obs(202, 2, "/dev/ttys011", "AAAAAAAA-0000-0000-0000-000000000002"), withSession(ev("SessionStart"), "shared"), t0)
	if a.InstanceID == b.InstanceID {
		t.Fatal("two terminals collapsed into one instance")
	}
	record(t, st, obs(202, 2, "/dev/ttys011", "AAAAAAAA-0000-0000-0000-000000000002"), withSession(ev("UserPromptSubmit"), "shared"), t0.Add(time.Second))
	ga, _ := st.Get(context.Background(), a.InstanceID)
	gb, _ := st.Get(context.Background(), b.InstanceID)
	if ga.State != Idle || gb.State != Working {
		t.Fatalf("states %q %q", ga.State, gb.State)
	}
}

// Acceptance test 6.
func TestProcessExitWithShellOpen(t *testing.T) {
	st := openTest(t)
	procs := &fakeProcs{m: map[int]process.Proc{
		301: {PID: 301, Start: 5, TTY: "/dev/ttys020"},
		300: {PID: 300, Start: 4, TTY: "/dev/ttys020"}, // the shell stays
	}}
	in := record(t, st, obs(301, 5, "/dev/ttys020", ""), withSession(ev("UserPromptSubmit"), "s"), t0)
	list, _ := st.List(context.Background())
	list = Reconcile(context.Background(), st, procs, list, t0.Add(time.Minute))
	if list[0].State != Working || list[0].Liveness != Alive {
		t.Fatalf("live instance: %q %q", list[0].State, list[0].Liveness)
	}
	procs.mu.Lock()
	delete(procs.m, 301)
	procs.mu.Unlock()
	list, _ = st.List(context.Background())
	list = Reconcile(context.Background(), st, procs, list, t0.Add(2*time.Minute))
	if list[0].State != Ended || list[0].Reason != ReasonProcessExit {
		t.Fatalf("after exit: %q %q", list[0].State, list[0].Reason)
	}
	stored, _ := st.Get(context.Background(), in.InstanceID)
	if stored.State != Ended {
		t.Fatal("process exit not persisted")
	}
}

// Acceptance test 7.
func TestPIDReuseDoesNotRevive(t *testing.T) {
	st := openTest(t)
	procs := &fakeProcs{m: map[int]process.Proc{400: {PID: 400, Start: 10, TTY: "/dev/ttys030"}}}
	old := record(t, st, obs(400, 10, "/dev/ttys030", ""), withSession(ev("UserPromptSubmit"), "s"), t0)
	// The PID now belongs to a new process on the same TTY.
	procs.m[400] = process.Proc{PID: 400, Start: 99, TTY: "/dev/ttys030"}
	list, _ := st.List(context.Background())
	list = Reconcile(context.Background(), st, procs, list, t0.Add(time.Minute))
	if list[0].State != Ended {
		t.Fatalf("reused PID: state %q", list[0].State)
	}
	// An event from the new process, even with the same session ID, makes a
	// new instance and does not revive the old one.
	nw := record(t, st, obs(400, 99, "/dev/ttys030", ""), withSession(ev("SessionStart"), "s"), t0.Add(2*time.Minute))
	if nw.InstanceID == old.InstanceID {
		t.Fatal("new process reused the old instance")
	}
	o, _ := st.Get(context.Background(), old.InstanceID)
	if o.State != Ended {
		t.Fatalf("old instance revived: %q", o.State)
	}
}

// Acceptance test 11.
func TestUnknownOwnerIsProvisional(t *testing.T) {
	st := openTest(t)
	unknown := Observation{OwnerMethod: process.MethodNotFound, TerminalKind: TerminalUnknown}
	in := record(t, st, unknown, withSession(ev("UserPromptSubmit"), "p"), t0)
	if in.OwnerPID != 0 || in.Liveness != Unknown {
		t.Fatalf("owner %d liveness %q", in.OwnerPID, in.Liveness)
	}
	list, _ := st.List(context.Background())
	list = Reconcile(context.Background(), st, &fakeProcs{}, list, t0)
	if list[0].Liveness != Unknown || list[0].State != Working || !list[0].Live() {
		t.Fatalf("reconciled: %+v", list[0])
	}
	// Stronger evidence later adopts the provisional row.
	again := record(t, st, Observation{OwnerPID: 9, OwnerStart: 9, OwnerMethod: process.MethodNameAncestor, TerminalKind: TerminalUnknown},
		withSession(ev("Stop"), "p"), t0.Add(time.Second))
	if again.InstanceID != in.InstanceID || again.OwnerPID != 9 {
		t.Fatalf("provisional row not adopted: %s vs %s", again.InstanceID, in.InstanceID)
	}
	all, _ := st.List(context.Background())
	if len(all) != 1 {
		t.Fatalf("%d rows, want 1", len(all))
	}
}

func TestNewSessionSupersedesOld(t *testing.T) {
	st := openTest(t)
	o := obs(500, 50, "/dev/ttys040", "")
	a := record(t, st, o, withSession(ev("UserPromptSubmit"), "before-clear"), t0)
	record(t, st, o, withSession(ev("SessionStart"), "after-clear"), t0.Add(time.Second))
	got, _ := st.Get(context.Background(), a.InstanceID)
	if got.State != Ended || got.Reason != ReasonSuperseded {
		t.Fatalf("old session: %q %q", got.State, got.Reason)
	}
}

// Acceptance test 8 (in-process part; run it with -race).
func TestConcurrentWritersLoseNoUpdates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "st")
	ctx := context.Background()
	first, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	const writers, perWriter = 8, 25
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			st, err := Open(ctx, dir) // one connection per writer, like one process per hook
			if err != nil {
				errs <- err
				return
			}
			defer st.Close()
			for i := 0; i < perWriter; i++ {
				// Half of the writers update one shared instance.
				sid, o := "shared", obs(600, 60, "/dev/ttys050", "")
				if w%2 == 1 {
					sid, o = fmt.Sprintf("own-%d", w), obs(700+w, 70, fmt.Sprintf("/dev/ttys06%d", w), "")
				}
				e := withSession(ev("PreToolUse", tool("Bash", fmt.Sprintf("t-%d-%d", w, i))), sid)
				if _, err := st.Record(ctx, o, e, true, time.Now()); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	st, _ := Open(ctx, dir)
	defer st.Close()
	stats, _ := st.Stats(ctx)
	if stats.Events != writers*perWriter {
		t.Fatalf("%d events, want %d", stats.Events, writers*perWriter)
	}
	if stats.Instances != 1+writers/2 {
		t.Fatalf("%d instances, want %d", stats.Instances, 1+writers/2)
	}
	list, _ := st.List(ctx)
	for _, in := range list {
		evs, _ := st.Events(ctx, in.InstanceID, 1000)
		if in.LastSeq != evs[len(evs)-1].Seq {
			t.Fatalf("instance %s last_seq %d, latest event %d", in.InstanceID, in.LastSeq, evs[len(evs)-1].Seq)
		}
	}
}

func TestFilePermissions(t *testing.T) {
	st := openTest(t)
	record(t, st, obs(1, 1, "", ""), withSession(ev("SessionStart"), "x"), t0)
	fi, err := os.Stat(st.Path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %o", fi.Mode().Perm())
	}
	di, _ := os.Stat(filepath.Dir(st.Path))
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %o", di.Mode().Perm())
	}
}

func TestGetPrefix(t *testing.T) {
	st := openTest(t)
	in := record(t, st, obs(1, 1, "", ""), withSession(ev("SessionStart"), "x"), t0)
	got, err := st.Get(context.Background(), in.InstanceID[:6])
	if err != nil || got.InstanceID != in.InstanceID {
		t.Fatalf("prefix lookup: %v", err)
	}
	if _, err := st.Get(context.Background(), "%"); err != ErrNotFound {
		t.Fatalf("wildcard lookup: %v", err)
	}
}

func TestPrune(t *testing.T) {
	st := openTest(t)
	old := t0.Add(-60 * 24 * time.Hour)
	record(t, st, obs(1, 1, "", ""), withSession(ev("SessionEnd"), "old"), old)
	record(t, st, obs(2, 2, "", ""), withSession(ev("SessionStart"), "new"), t0)
	if err := st.Prune(context.Background(), t0); err != nil {
		t.Fatal(err)
	}
	stats, _ := st.Stats(context.Background())
	if stats.Instances != 1 || stats.Events != 1 {
		t.Fatalf("after prune: %+v", stats)
	}
}

func TestRetryBusy(t *testing.T) {
	calls := 0
	err := retryBusy(context.Background(), func() error {
		calls++
		if calls < 4 {
			return fmt.Errorf("read schema version: database is locked (5) (SQLITE_BUSY)")
		}
		return nil
	})
	if err != nil || calls != 4 {
		t.Fatalf("busy errors: err %v after %d calls", err, calls)
	}
	calls = 0
	other := fmt.Errorf("disk I/O error")
	if err := retryBusy(context.Background(), func() error { calls++; return other }); err != other || calls != 1 {
		t.Fatalf("other error: %v after %d calls", err, calls)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = retryBusy(ctx, func() error { return fmt.Errorf("SQLITE_BUSY") })
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("bounded retry: %v after %s", err, time.Since(start))
	}
}
