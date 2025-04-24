package atarax

import (
	"context"
	"errors"
	"iter"
	"sync/atomic"
	"time"
)

type JobID int64

var _globalID JobID

func generateID() JobID {
	return JobID(atomic.AddInt64((*int64)(&_globalID), 1))
}

type Runnable interface {
	Run(ctx context.Context) error
}

type RunnableFunc func(ctx context.Context) error

func (f RunnableFunc) Run(ctx context.Context) error {
	return f(ctx)
}

type Job struct {
	timeout  time.Duration
	interval time.Duration
	id       JobID
	task     Runnable
}

func (j *Job) ID() JobID {
	return j.id
}

var ErrJobTimeout = errors.New("job timeout")

type runConfig struct {
	// reserved
}

type JobRunOpt interface {
	apply(runConfig) runConfig
}

func (j *Job) Run(ctx context.Context, opts ...JobRunOpt) error {
	c := runConfig{}
	for _, opt := range opts {
		c = opt.apply(c)
	}

	t := time.Now()
	_jobsWorking.Add(ctx, 1)

	err := j.run(ctx)

	_jobsWorking.Add(ctx, -1)
	f := time.Since(t).Milliseconds()

	if err != nil {
		if errors.Is(err, ErrJobTimeout) {
			_jobTimings.Record(ctx, f, _attributeTimeout)
			return err
		}

		_jobTimings.Record(ctx, f, _attributeErr)
		return err
	}
	_jobTimings.Record(ctx, f, _attributeOK)

	return nil
}

func (j *Job) run(ctx context.Context) error {
	err := j.task.Run(ctx)
	return err
}

func (j *Job) Timeout() time.Duration {
	return j.timeout
}

func (j *Job) Interval() time.Duration {
	return j.interval
}

func (j *Job) Clone() *Job {
	return &Job{
		timeout:  j.timeout,
		interval: j.interval,
		id:       generateID(),
		task:     j.task,
	}
}

func (j *Job) Task() Runnable {
	return j.task
}

type JobOpt func(*Job)

func NewJob(task Runnable, timeout, interval time.Duration, opts ...JobOpt) *Job {
	j := &Job{
		timeout:  timeout,
		interval: interval,
		id:       generateID(),
		task:     task,
	}

	for _, opt := range opts {
		opt(j)
	}

	return j
}

var (
	ErrJobExists   = errors.New("job already exists")
	ErrJobNotFound = errors.New("job not found")
)

// Scheduler is the interface for schedule periodically executing jobs. It contains the necessary minimum
// providing the Schedule function and an adaptivity.
type Scheduler interface {
	// Jobs returns an iterator to all the jobs that the scheduler processes currently.
	Jobs() iter.Seq2[JobID, *Job]

	// Schedule starts scheduling tasks containing in the pool. It should be run synchronously.
	//
	// The function should return an error if the scheduling can't be completed anymore.
	// Context cancellation should provide graceful shutdown for all the running jobs and then
	// return an error from the context using [context.Cause].
	//
	// Task errors don't influence Scheduler error.
	Schedule(context.Context) error

	// Add adds a new job into the pool to schedule. If job's ID collides with
	// another job containing in the pool, the function should return ErrJobExists.
	Add(*Job) error

	// Remove finds the job with a given ID and pulls it from the scheduling pool.
	// If there's no the job with such ID, ErrJobNotFound is returned.
	Remove(JobID) error
}
