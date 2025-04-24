package workerpool

import (
	"context"
	"iter"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pelageech/atarax"
	pkgsync "github.com/pelageech/atarax/pkg/sync"
	"github.com/pelageech/atarax/pkg/sync/lockfree"
	"github.com/pelageech/atarax/pkg/timewheel"

	"github.com/panjf2000/ants/v2"
	"go.opentelemetry.io/otel/metric"
)

const (
	System = "WorkerPool"
)

type Scheduler struct {
	scheduleCtx context.Context

	mu            sync.RWMutex
	jobs          map[atarax.JobID]*atarax.Job
	jobsProcessed map[atarax.JobID]chan int64
	jobsDone      map[atarax.JobID]chan struct{}

	workers int64

	jobsChan   *lockfree.Queue[jobGen]
	spinCond   chan struct{}
	stack      *pkgsync.Stack[chan struct{}]
	condSleeps atomic.Int64

	wg *sync.WaitGroup

	schedulerStarted chan struct{}

	serviceName        string
	jobsCount          metric.Int64UpDownCounter
	jobsWaiting        metric.Int64UpDownCounter
	jobsFullPath       metric.Int64Histogram
	jobsTicksDropped   metric.Int64Counter
	jobsSpinLockMiss   metric.Int64Counter
	jobsSpinLockWins   metric.Int64Counter
	jobsSpinLockSleeps metric.Int64Counter
	workersCount       metric.Int64UpDownCounter
	workerSpawnNew     metric.Int64Counter
	workersSleeping    metric.Int64UpDownCounter
	chanJobsLen        metric.Int64Gauge
}

type Opt func(*Scheduler)

// WithServiceName is used for a separate attribute for OTel metrics.
func WithServiceName(serviceName string) Opt {
	return func(s *Scheduler) {
		s.serviceName = serviceName
	}
}

// WithWorkersCount sets an initial worker pool size.
func WithWorkersCount(count int64) Opt {
	return func(s *Scheduler) {
		s.workers = count
	}
}

func init() {
	timewheel.DefaultTimeWheel.Stop()
	timewheel.DefaultTimeWheel, _ = timewheel.NewTimeWheel(1*time.Millisecond, 80<<10)
	timewheel.DefaultTimeWheel.Start()
}

func NewScheduler(jobs []*atarax.Job, opts ...Opt) *Scheduler {
	s := &Scheduler{
		scheduleCtx:      context.Background(),
		jobs:             make(map[atarax.JobID]*atarax.Job, len(jobs)),
		jobsDone:         make(map[atarax.JobID]chan struct{}, len(jobs)),
		jobsProcessed:    make(map[atarax.JobID]chan int64, len(jobs)),
		jobsChan:         lockfree.NewQueue[jobGen](),
		spinCond:         make(chan struct{}),
		stack:            pkgsync.NewStack[chan struct{}](),
		schedulerStarted: make(chan struct{}, 1),
		workers:          10,
		wg:               &sync.WaitGroup{},
		serviceName:      "default",
	}

	// instrument OpenTelemetry
	atarax.SetSystem(System)
	atarax.SetVersion(atarax.Version())
	atarax.SetService(s.serviceName)
	atarax.InstrumentMetrics()

	m := atarax.Meter()
	s.jobsFullPath, _ = m.Int64Histogram(atarax.MeterPrefix+"jobs.fullpath", metric.WithUnit("ms"))
	s.jobsWaiting, _ = m.Int64UpDownCounter(atarax.MeterPrefix + "jobs.waiting")
	s.jobsCount, _ = m.Int64UpDownCounter(atarax.MeterPrefix + "jobs.count")
	s.jobsTicksDropped, _ = m.Int64Counter(atarax.MeterPrefix + "jobs.drops")
	s.jobsSpinLockMiss, _ = m.Int64Counter(atarax.MeterPrefix + "workers.miss")
	s.jobsSpinLockWins, _ = m.Int64Counter(atarax.MeterPrefix + "workers.wins")
	s.jobsSpinLockSleeps, _ = m.Int64Counter(atarax.MeterPrefix + "workers.sleep")
	s.workersCount, _ = m.Int64UpDownCounter(atarax.MeterPrefix + "workers")
	s.workerSpawnNew, _ = m.Int64Counter(atarax.MeterPrefix + "workers.spawn")
	s.workersSleeping, _ = m.Int64UpDownCounter(atarax.MeterPrefix + "workers.sleeping")
	s.chanJobsLen, _ = m.Int64Gauge(atarax.MeterPrefix + "jobs.buffer_len")

	for _, opt := range opts {
		opt(s)
	}

	for _, job := range jobs {
		_ = s.addThreadUnsafe(job)
	}

	return s
}

func (s *Scheduler) Jobs() iter.Seq2[atarax.JobID, *atarax.Job] {
	return func(yield func(atarax.JobID, *atarax.Job) bool) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		for k, v := range s.jobs {
			if !yield(k, v) {
				break
			}
		}
	}
}

func (s *Scheduler) Schedule(ctx context.Context) error {
	s.mu.Lock()
	s.scheduleCtx = ctx

	for _, job := range s.jobs {
		s.wg.Add(1)
		ants.Submit(func() { s.runJob(ctx, job) })
	}

	for range s.workers {
		ants.Submit(func() { s.runWorker(ctx) })
	}

	ants.Submit(func() {
		ti := timewheel.NewTicker(10 * time.Millisecond)
		defer ti.Stop()

		for range ti.C {
			if ctx.Err() != nil {
				return
			}
			l := s.jobsChan.Len()
			s.chanJobsLen.Record(ctx, l)
			if l > 0 {
				// todo: it's too simple formula
				toWakeUp := l/100 + l%100
				for i := int64(0); i < toWakeUp; i++ {
					if s.condSleeps.Load() < 5 {
						s.workerSpawnNew.Add(ctx, 1)
						ants.Submit(func() { s.runWorker(ctx) })
					} else {
						ch := s.stack.Pop()
						if ch == nil {
							i--
							continue
						}
						_, ok := <-ch
						if !ok {
							i--
							continue
						}
						s.condSleeps.Add(-1)
						s.workersSleeping.Add(ctx, -1)
					}
				}
			}
		}
	})

	close(s.schedulerStarted)
	s.mu.Unlock()
	<-ctx.Done()
	s.wg.Wait()
	return nil
}

type jobGen struct {
	gen  int64
	send chan int64
	job  *atarax.Job
}

const (
	_maxSpinLocksSleep = 4
)

func (s *Scheduler) runWorker(ctx context.Context) {
	s.workersCount.Add(ctx, 1)
	defer s.workersCount.Add(ctx, -1)
	spinCond := make(chan struct{})
	spins := 0
	for {
		var ok bool
		job, ok := s.jobsChan.Dequeue()
		if !ok {
			spins++
			s.jobsSpinLockMiss.Add(ctx, 1)
			if spins == _maxSpinLocksSleep {
				s.jobsSpinLockSleeps.Add(ctx, 1)
				spins = 0
				s.condSleeps.Add(1)
				s.workersSleeping.Add(ctx, 1)
				s.stack.Push(spinCond)
				select {
				case spinCond <- struct{}{}:
				case <-ctx.Done():
					close(spinCond)
					return
				case <-timewheel.After(20*time.Second + rand.N(12*time.Second)):
					s.condSleeps.Add(-1)
					s.workersSleeping.Add(ctx, -1)
					close(spinCond)
					return
				}
			} else {
				runtime.Gosched()
			}
			continue
		}
		s.jobsSpinLockWins.Add(ctx, 1)
		t := time.Now()

		s.jobsWaiting.Add(ctx, -1)

		_ = job.job.Run(ctx)
		job.send <- job.gen
		s.jobsFullPath.Record(ctx, time.Since(t).Milliseconds())
	}
}

func (s *Scheduler) runJob(ctx context.Context, job *atarax.Job) {
	defer s.jobsCount.Add(ctx, -1)
	s.jobsCount.Add(ctx, 1)
	defer s.wg.Done()

	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	timer := timewheel.NewTimer(time.Duration(rng.Int64N(int64(job.Interval()))))
	defer timer.Stop()

	s.mu.Lock()
	s.jobsProcessed[job.ID()] = make(chan int64, 3)
	s.jobsDone[job.ID()] = make(chan struct{})

	defer func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		delete(s.jobsProcessed, job.ID())
		delete(s.jobsDone, job.ID())
	}()

	chProcessed := s.jobsProcessed[job.ID()]
	chDone := s.jobsDone[job.ID()]
	s.mu.Unlock()
	gen := int64(0)
	chProcessed <- gen
	last := time.Duration(0)
	for {
		<-timer.C

		if job.Interval()-job.Timeout() == 0 {
			timer.Reset(job.Interval())
		} else {
			// power of two random choice
			last = timer.ResetBest(
				job.Interval()-last+time.Duration(rng.Int64N(int64(job.Interval()-job.Timeout()))),
				job.Interval()-last+time.Duration(rng.Int64N(int64(job.Interval()-job.Timeout()))),
			) - (job.Interval() - last)
		}

		select {
		case <-ctx.Done():
			return
		case <-chProcessed:
			gen++

			select {
			case <-ctx.Done():
				return
			case <-chDone:
				return
			default:
				s.jobsChan.Enqueue(jobGen{gen: gen, job: job, send: chProcessed})
				s.jobsWaiting.Add(ctx, 1)
			}
		case <-chDone:
			return
		default:
			s.jobsTicksDropped.Add(ctx, 1)
		}
	}
}

func (s *Scheduler) Add(job *atarax.Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.addThreadUnsafe(job)
}

func (s *Scheduler) addThreadUnsafe(job *atarax.Job) error {
	if _, ok := s.jobs[job.ID()]; ok {
		return atarax.ErrJobExists
	}

	s.jobs[job.ID()] = job
	// if scheduler is not started, we should prevent double job start.
	// So Schedule func will create a goroutine
	select {
	case <-s.schedulerStarted:
	default:
		return nil
	}

	s.wg.Add(1)
	ants.Submit(func() { s.runJob(s.scheduleCtx, job) })

	return nil
}

func (s *Scheduler) Remove(id atarax.JobID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.removeThreadUnsafe(id)
}

func (s *Scheduler) removeThreadUnsafe(id atarax.JobID) error {
	if _, ok := s.jobs[id]; !ok {
		return atarax.ErrJobNotFound
	}
	delete(s.jobs, id)
	if s.jobsDone[id] != nil {
		close(s.jobsDone[id])
	}

	return nil
}

func (s *Scheduler) BatchAdd(jobs iter.Seq[*atarax.Job]) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for job := range jobs {
		s.addThreadUnsafe(job)
	}
}

func (s *Scheduler) BatchRemove(i iter.Seq[atarax.JobID]) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for job := range i {
		s.removeThreadUnsafe(job)
	}
}
