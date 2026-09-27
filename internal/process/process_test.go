package process

import (
	"errors"
	"os"
	"runtime"
	"testing"
)

type fake map[int]Proc

func (f fake) Lookup(pid int) (Proc, error) {
	if p, ok := f[pid]; ok {
		return p, nil
	}
	return Proc{}, ErrNotFound
}

func chain() fake {
	return fake{
		10: {PID: 10, PPID: 9, Comm: "cwatch"},                                     // the hook
		9:  {PID: 9, PPID: 8, Comm: "sh"},                                          // shell form
		8:  {PID: 8, PPID: 7, Comm: "claude.exe", Start: 800, TTY: "/dev/ttys004"}, // owner
		7:  {PID: 7, PPID: 6, Comm: "zsh", Start: 700, TTY: "/dev/ttys004"},
		6:  {PID: 6, PPID: 1, Comm: "login", Start: 600, TTY: "/dev/ttys004"},
	}
}

func TestFindOwnerByName(t *testing.T) {
	o := FindOwner(chain(), 10, 0)
	if o.PID != 8 || o.Start != 800 || o.TTY != "/dev/ttys004" || o.Method != MethodNameAncestor {
		t.Fatalf("owner %+v", o)
	}
}

func TestFindOwnerByHint(t *testing.T) {
	f := chain()
	f[8] = Proc{PID: 8, PPID: 7, Comm: "node", Start: 800, TTY: "/dev/ttys004"}
	o := FindOwner(f, 10, 8)
	if o.PID != 8 || o.Method != MethodEnvAncestor {
		t.Fatalf("owner %+v", o)
	}
	// A hint that is not an ancestor is ignored.
	if o := FindOwner(f, 10, 12345); o.Known() {
		t.Fatalf("non-ancestor hint accepted: %+v", o)
	}
}

func TestFindOwnerNeverPicksTTYAncestor(t *testing.T) {
	f := chain()
	delete(f, 8)
	f[9] = Proc{PID: 9, PPID: 7, Comm: "sh"}
	o := FindOwner(f, 10, 0)
	if o.Known() || o.Method != MethodNotFound {
		t.Fatalf("picked an unrelated ancestor: %+v", o)
	}
}

func TestFindOwnerLookupError(t *testing.T) {
	if o := FindOwner(fake{}, 10, 0); o.Known() || o.Method != MethodError {
		t.Fatalf("owner %+v", o)
	}
}

func TestCheck(t *testing.T) {
	f := chain()
	if l, _ := Check(f, 8, 800); l != Alive {
		t.Fatalf("alive: %s", l)
	}
	if l, _ := Check(f, 8, 801); l != Dead {
		t.Fatalf("reused PID: %s", l)
	}
	if l, _ := Check(f, 99, 1); l != Dead {
		t.Fatalf("missing: %s", l)
	}
	if l, _ := Check(f, 0, 0); l != Unknown {
		t.Fatalf("no owner: %s", l)
	}
	if l, _ := Check(errInspector{}, 8, 800); l != Unknown {
		t.Fatalf("lookup error: %s", l)
	}
}

type errInspector struct{}

func (errInspector) Lookup(int) (Proc, error) { return Proc{}, errors.New("EPERM") }

func TestSystemLookup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	p, err := System{}.Lookup(os.Getpid())
	if err != nil || p.PID != os.Getpid() || p.PPID != os.Getppid() || p.Start <= 0 {
		t.Fatalf("self: %+v %v", p, err)
	}
	if _, err := (System{}).Lookup(0x7ffffff0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing PID: %v", err)
	}
	if l, _ := Check(System{}, os.Getpid(), p.Start+1); l != Dead {
		t.Fatalf("start mismatch: %s", l)
	}
}
