package state

import (
	"context"
	"time"

	"cwatch/internal/process"
)

// Reconcile checks the owning process of each instance. It marks an
// instance ended when its process definitively no longer exists, or when the
// PID now belongs to a different process. It never infers an exit from
// silence or age.
func Reconcile(ctx context.Context, s *Store, insp process.Inspector, list []Instance, now time.Time) []Instance {
	out := make([]Instance, 0, len(list))
	for _, in := range list {
		if in.OwnerPID <= 0 || in.OwnerStart <= 0 {
			in.Liveness = Unknown
			out = append(out, in)
			continue
		}
		live, _ := process.Check(insp, in.OwnerPID, in.OwnerStart)
		switch live {
		case process.Alive:
			in.Liveness = Alive
		case process.Dead:
			in.Liveness = Dead
			if in.State != Ended {
				if s != nil {
					if ok, err := s.MarkProcessExit(ctx, in, now); err == nil && ok {
						in = MarkProcessExit(in, now)
					} else if fresh, err := s.Get(ctx, in.InstanceID); err == nil {
						// A new event changed the row. Show the stored row.
						in = fresh
						in.Liveness = Dead
					}
				} else {
					in = MarkProcessExit(in, now)
				}
			}
		default:
			in.Liveness = Unknown
		}
		out = append(out, in)
	}
	return out
}
