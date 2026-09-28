package scheduler

import (
	"context"
	"fmt"
)

func (s *Scheduler) catchUpLocked(ctx context.Context) error {
	now := s.now()
	for _, t := range s.triggers {
		prev := previousFire(now, t.hour, t.minute, s.loc)
		last, ok, err := s.getLastFire(ctx, t.jobType)
		if err != nil {
			return err
		}
		if !ok {
			if err := s.setLastFire(ctx, t.jobType, prev); err != nil {
				return err
			}
			s.log.Info("scheduler: catch-up watermark (first boot)", "job", t.jobType, "fire", prev)
			continue
		}
		if !last.Before(prev) {
			continue
		}
		if err := s.enqueueTrigger(ctx, t.jobType, "catchup"); err != nil {
			return fmt.Errorf("scheduler: catch-up %s: %w", t.jobType, err)
		}
		if err := s.setLastFire(ctx, t.jobType, prev); err != nil {
			return err
		}
		s.log.Info("scheduler: catch-up enqueued missed fire", "job", t.jobType, "fire", prev)
	}
	return nil
}
