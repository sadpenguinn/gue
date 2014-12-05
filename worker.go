package que

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type WorkFunc func(j *Job) error

type WorkMap map[string]WorkFunc

type Worker struct {
	Interval time.Duration
	Queue    string

	c *Client
	m WorkMap

	mu   sync.Mutex
	done bool
	ch   chan struct{}
}

func NewWorker(c *Client, m WorkMap) *Worker {
	return &Worker{
		Interval: 5 * time.Second,
		c:        c,
		m:        m,
		ch:       make(chan struct{}),
	}
}

func (w *Worker) Work() {
	for {
		select {
		case <-w.ch:
			log.Println("worker done")
			return
		case <-time.After(w.Interval):
			for {
				if didWork := w.workOne(); !didWork {
					break // didn't do any work, go back to sleep
				}
			}
		}
	}
}

func (w *Worker) workOne() (didWork bool) {
	j, err := w.c.LockJob(w.Queue)
	if err != nil {
		log.Printf("attempting to lock job: %v", err)
		return
	}
	if j == nil {
		return // no job was available
	}
	defer j.Done()

	didWork = true

	wf, ok := w.m[j.Type]
	if !ok {
		if err = j.Error(fmt.Sprintf("unknown job type: %q", j.Type)); err != nil {
			log.Printf("attempting to save error on job %d: %v", j.ID, err)
		}
		return
	}

	if err = wf(j); err != nil {
		j.Error(err.Error())
		return
	}

	if err = j.Delete(); err != nil {
		log.Printf("attempting to delete job %d: %v", j.ID, err)
	}
	log.Printf("event=job_worked job_id=%d job_type=%s", j.ID, j.Type)
	return
}

func (w *Worker) Shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done {
		return
	}

	log.Println("worker shutting down gracefully...")
	w.ch <- struct{}{}
	w.done = true
	close(w.ch)
}

type WorkerPool struct {
	workers []*Worker

	mu   sync.Mutex
	done bool
}

func NewWorkerPool(c *Client, wm WorkMap, interval time.Duration, count int) (w *WorkerPool) {
	w.workers = make([]*Worker, count)

	for i := 0; i < count; i++ {
		w.workers[i] = NewWorker(c, wm)
		w.workers[i].Interval = interval
	}
	return
}

func (w *WorkerPool) Start() {
	for i := range w.workers {
		go w.workers[i].Work()
	}
}

func (w *WorkerPool) Shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done {
		return
	}
	var wg sync.WaitGroup
	wg.Add(len(w.workers))

	for _, worker := range w.workers {
		go func(worker *Worker) {
			worker.Shutdown()
			wg.Done()
		}(worker)
	}
	wg.Wait()
	w.done = true
}
