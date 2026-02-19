package workerpool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pelageech/atarax"
	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
)

func TestWorkerPool(t *testing.T) {
	const toComplete = int64(5)

	ctx := t.Context()

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

	ctx := t.Context()

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

func TestHedgedTask(t *testing.T) {
	ctx := t.Context()

	sch := NewScheduler(nil, WithHedged(true))
	go sch.Schedule(ctx)

	ch1 := make(chan struct{}, 1)
	answer := make(chan int, 1)

	const toDone = 1
	doneTasks := &atomic.Int64{}

	var job *atarax.Job

	task := atarax.NewRunnableCallback[int](
		func(ctx context.Context) (int, error) {
			select {
			case ch1 <- struct{}{}:
				time.Sleep(10000 * time.Millisecond)
				t.Log("first error")
				return 0, errors.New("error")
			default:
				t.Log("task done, go to callback")
				return 42, nil
			}
		},
		func(ctx context.Context, i int) error {
			t.Log("callback done")
			answer <- i
			done := doneTasks.Add(1)
			if done == toDone {
				err := sch.Remove(job.ID())
				assert.NoError(t, err)
			}
			return nil
		},
	)

	job = atarax.NewJob(task, 1000*time.Millisecond, 2000*time.Millisecond)

	err := sch.Add(job)
	require.NoError(t, err)

	for range toDone {
		select {
		case ans := <-answer:
			assert.Equal(t, 42, ans)
		case <-time.After(800 * time.Millisecond):
			t.Fatal("timeout")
		}
	}
}
