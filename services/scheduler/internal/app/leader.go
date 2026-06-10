package app

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/RAF-SI-2025/Banka-3-Backend/pkg/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// runJobs starts every registered job in its own goroutine and blocks
// until ctx is cancelled (shutdown or lost leadership), at which point
// every job loop returns and runJobs unblocks.
func (a *App) runJobs(ctx context.Context) {
	jobs := a.buildJobs()
	a.log.InfoContext(ctx, "starting scheduled jobs", "count", len(jobs))
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			j.run(ctx)
		}(j)
	}
	wg.Wait()
	a.log.InfoContext(ctx, "all scheduled jobs stopped")
}

// runWithLeaderElection runs the jobs gated by a k8s Lease when
// LEADER_ELECTION=true (production: N replicas, only the lease-holder
// fires timers). When false (docker-compose / single instance) it runs
// the jobs directly as the sole leader.
func (a *App) runWithLeaderElection(ctx context.Context) error {
	if !config.Bool("LEADER_ELECTION", false) {
		a.log.InfoContext(ctx, "leader election disabled; running as sole scheduler")
		a.runJobs(ctx)
		return nil
	}

	cfg, err := rest.InClusterConfig()
	if err != nil {
		a.log.ErrorContext(ctx, "in-cluster config failed", "err", err)
		return fmt.Errorf("in-cluster config: %w", err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		a.log.ErrorContext(ctx, "k8s client init failed", "err", err)
		return fmt.Errorf("k8s client: %w", err)
	}

	id := os.Getenv("HOSTNAME")
	if id == "" {
		id = "scheduler"
	}
	ns := config.String("POD_NAMESPACE", "raf-banka")
	leaseName := config.String("LEASE_NAME", "scheduler-leader")

	lock := &resourcelock.LeaseLock{
		LeaseMeta:  metav1.ObjectMeta{Name: leaseName, Namespace: ns},
		Client:     client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{Identity: id},
	}

	a.log.InfoContext(ctx, "starting leader election", "lease", leaseName, "namespace", ns, "identity", id)
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(c context.Context) {
				a.log.InfoContext(c, "acquired leadership; starting jobs", "identity", id)
				a.runJobs(c)
			},
			OnStoppedLeading: func() {
				a.log.Info("lost leadership; jobs stopped", "identity", id)
			},
			OnNewLeader: func(identity string) {
				if identity != id {
					a.log.Info("observing current leader", "leader", identity)
				}
			},
		},
	})
	return nil
}
