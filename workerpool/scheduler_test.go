package workerpool

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pelageech/atarax"

	"github.com/stretchr/testify/require"
)

func TestWorkerPool(t *testing.T) {
	const toComplete = int64(5)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sch := NewScheduler(nil)
	go func() {
		sch.Schedule(ctx)
	}()
	var runnable atarax.Runnable
	c := atomic.Int64{}
	doneTasks := make(chan struct{})
	waitTask := make(chan struct{})

	runnable = atarax.RunnableFunc(func(ctx context.Context) error {
		if c.Add(1) == toComplete {
			t.Log("task complete, continue main loop")
			close(doneTasks)
			<-waitTask
		}
		return nil
	})

	job := atarax.NewJob(runnable, 100*time.Millisecond, 200*time.Millisecond)
	require.NoError(t, sch.Add(job))

	t.Log("waiting tasks")
	<-doneTasks
	t.Log("remove jobs")
	require.NoError(t, sch.Remove(job.ID()))
	require.Equal(t, toComplete, c.Load())
	t.Log("closing waiter")
	close(waitTask)
	// can be optimized with synctime in go1.24
	t.Log("waiting for 1s and check again")
	time.Sleep(1000 * time.Millisecond)
	require.Equal(t, toComplete, c.Load())
}

func TestWorkerPoolConcurrent(t *testing.T) {
	const (
		jobsCount  = 5
		toComplete = 20
	)

	jobs := [jobsCount]*atarax.Job{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// start scheduler
	sch := NewScheduler(nil, WithWorkersCount(jobsCount))
	go func() {
		sch.Schedule(ctx)
	}()

	var runnable atarax.Runnable
	c := atomic.Int64{}
	doneTasks := make(chan struct{}, jobsCount)
	afterDoneTasks := make(chan struct{}, jobsCount)
	continueTask := make(chan struct{})

	onDone := atomic.Pointer[func()]{}
	noop := func() {}
	onDone.Store(&noop)

	runnable = atarax.RunnableFunc(func(ctx context.Context) error {
		done := *onDone.Load()
		defer done()

		// if counter is done, continue main func
		if newC := c.Add(1); newC >= toComplete {
			doneTasks <- struct{}{}
			t.Log("task complete, continue main loop")
			<-continueTask
			afterDoneTasks <- struct{}{}
			return nil
		}
		return nil
	})

	for i := range jobsCount {
		job := atarax.NewJob(runnable, 100*time.Millisecond, 200*time.Millisecond)
		require.NoError(t, sch.Add(job))
		jobs[i] = job
	}

	t.Log("wait signal when a counter is done")
	for range jobsCount {
		<-doneTasks
	}

	t.Log("remove jobs, then continue tasks")
	for _, job := range jobs {
		require.NoError(t, sch.Remove(job.ID()))
	}
	close(continueTask)

	t.Log("wait when tasks are done")
	for range jobsCount {
		<-afterDoneTasks
	}

	executed := atomic.Int64{}
	final := func() {
		executed.Add(1)
	}
	onDone.Store(&final)

	t.Log("wait for 1s and check again")
	// can be optimized with synctime in go1.24
	time.Sleep(1 * time.Second)
	require.Zero(t, executed.Load())
}
