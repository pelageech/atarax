package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/pelageech/atarax"
	"github.com/pelageech/atarax/workerpool"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var sched atarax.Scheduler
	sched = workerpool.NewScheduler(nil)
	go sched.Schedule(ctx)

	now := time.Now()
	jobsComplete := 0
	sum := time.Duration(0)
	
	// runnable implements atarax.Runnable
	runnable := atarax.RunnableFunc(func(_ context.Context) error {
		t := time.Now()
		sub := t.Sub(now)
		sum += sub
		now = t

		jobsComplete++

		fmt.Println(sub)
		return nil
	})

	timeout, interval := time.Second, time.Second
	job := atarax.NewJob(runnable, timeout, interval)

	if err := sched.Add(job); err != nil {
		log.Fatal(err)
	}

	<-ctx.Done()
	fmt.Printf("Jobs complete: %d, duration sum: %v\n", jobsComplete, sum)
}
