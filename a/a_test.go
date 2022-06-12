package a

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLaunchGoroutineDataRace(t *testing.T) {
	// Tests that the order of execution of a goroutine and the code after
	// is indeterminate.
	// Create a data race where there are multiple paths through the function.
	var wasZeroCounts [100]uint64
	var notZeroCounts [100]uint64
	runtime.GOMAXPROCS(16)

	buggyFunc := func() {
		var data int
		go func() {
			for i := 0; i < 99; i++ {
				data++
			}
		}()
		if data == 0 {
			atomic.AddUint64(&wasZeroCounts[data], 1)
		} else {
			atomic.AddUint64(&notZeroCounts[data], 1)
		}
	}

	for i := 0; i < 100000000; i++ {
		buggyFunc()
	}

	for i := 0; i < 100; i++ {
		if wasZeroCounts[i] != 0 {
			fmt.Println("wasZero", i, wasZeroCounts[i])
		}
	}

	for i := 0; i < 100; i++ {
		if notZeroCounts[i] != 0 {
			fmt.Println("notZero", i, notZeroCounts[i])
		}
	}

	// Output:
	// wasZero 0 99999917
	// notZero 99 83
	//
	// This shows that in 99.9999% of the time, the code immediately after the
	// goroutine launch runs first.
	// And also, if the goroutine happens to run first, it will most likely
	// complete before the code after will run.
}

func TestDeadlock1(t *testing.T) {
	// Create a deadlock as demonstration.
	type val struct {
		mu    sync.Mutex
		value int
	}

	var wg sync.WaitGroup
	printSum := func(v1, v2 *val) {
		defer wg.Done()
		v1.mu.Lock()
		defer v1.mu.Unlock()

		time.Sleep(1 * time.Second)
		v2.mu.Lock()
		defer v2.mu.Unlock()

		fmt.Println("sum:", v1.value+v2.value)
	}

	var a, b val
	wg.Add(2)
	go printSum(&a, &b)
	go printSum(&b, &a)
	wg.Wait()

	// Thread 1 locks A
	// Thread 2 locks B
	// Thread 1 attempts to acquire lock B
	// Thread 2 attempts to acquire lock A
	// Both wait forever -- deadlock.

	// In theory this code might not deadlock.
	// Execution time of goroutines is not guaranteed,
	// so with some luck thread 1 could sleep 1 second and acquire both locks
	// before thread 2 has a chance to start.
}

func TestSyncCondSignal_FatalError(t *testing.T) {
	// fatal error: sync: unlock of unlocked mutex
	cond := sync.NewCond(&sync.Mutex{})
	go func() {
		fmt.Println("Goroutine start")
		cond.Wait()
		fmt.Println("Goroutine done")
	}()

	time.Sleep(time.Second)
	cond.Signal()
	time.Sleep(time.Second)
}

func TestSyncCondSignal_Fixed(t *testing.T) {
	// fatal error: sync: unlock of unlocked mutex
	cond := sync.NewCond(&sync.Mutex{})
	go func() {
		fmt.Println("Goroutine start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine done")
	}()

	time.Sleep(time.Second)
	cond.Signal()
	time.Sleep(time.Second)
}

func TestSyncCondSignal_BroadcastOne(t *testing.T) {
	// Broadcast works like signal when only one goroutine waits.
	cond := sync.NewCond(&sync.Mutex{})
	go func() {
		fmt.Println("Goroutine start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine done")
	}()

	time.Sleep(time.Second)
	cond.Broadcast()
	time.Sleep(time.Second)
}

func TestSyncCondSignal_SignalTwo(t *testing.T) {
	// Only one goroutine is unlocked by Signal
	// Example output:
	// Goroutine B start
	// Goroutine A start
	// Goroutine B done
	cond := sync.NewCond(&sync.Mutex{})
	go func() {
		fmt.Println("Goroutine A start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine A done")
	}()

	go func() {
		fmt.Println("Goroutine B start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine B done")
	}()

	time.Sleep(time.Second)
	cond.Signal()
	time.Sleep(time.Second)
}

func TestSyncCondSignal_BroadcastTwo(t *testing.T) {
	// Both goroutines are unlocked by Broadcast.
	// Example output:
	// Goroutine B start
	// Goroutine A start
	// Goroutine B done
	// Goroutine A done
	cond := sync.NewCond(&sync.Mutex{})
	go func() {
		fmt.Println("Goroutine A start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine A done")
	}()

	go func() {
		fmt.Println("Goroutine B start")
		cond.L.Lock()
		cond.Wait()
		cond.L.Unlock()
		fmt.Println("Goroutine B done")
	}()

	time.Sleep(time.Second)
	cond.Broadcast()
	time.Sleep(time.Second)
}

// Source: Concurrency in Go
func TestLiveLock1(t *testing.T) {
	cadence := sync.NewCond(&sync.Mutex{})
	go func() {
		for range time.Tick(1 * time.Millisecond) {
			cadence.Broadcast()
		}
	}()

	takeStep := func() {
		cadence.L.Lock()
		cadence.Wait()
		cadence.L.Unlock()
	}

	tryDir := func(dirName string, dir *int32, out *bytes.Buffer) bool {
		fmt.Fprintf(out, " %v", dirName)
		atomic.AddInt32(dir, 1)
		takeStep()
		if atomic.LoadInt32(dir) == 1 {
			fmt.Fprintf(out, ". Success!")
			return true
		}
		takeStep()
		atomic.AddInt32(dir, -1)
		return false
	}

	var left, right int32
	tryLeft := func(out *bytes.Buffer) bool {
		return tryDir("left", &left, out)
	}
	tryRight := func(out *bytes.Buffer) bool {
		return tryDir("right", &right, out)
	}

	walk := func(walking *sync.WaitGroup, name string) {
		var out bytes.Buffer
		defer func() {
			fmt.Println(out.String())
		}()
		defer walking.Done()
		fmt.Fprintf(&out, "%v is trying to scoot:", name)
		for i := 0; i < 5; i++ {
			if tryLeft(&out) || tryRight(&out) {
				return
			}
		}
		fmt.Fprintf(&out, "\n%v tosses her hands up in exasperation!", name)
	}

	var peopleInHallway sync.WaitGroup
	peopleInHallway.Add(2)
	go walk(&peopleInHallway, "Alice")
	go walk(&peopleInHallway, "Barbara")
	peopleInHallway.Wait()

}

// 160 ns/op
func BenchmarkChannels1(b *testing.B) {
	var wg sync.WaitGroup
	wg.Add(2)

	ch := make(chan int)
	go func() { // receiver
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			<-ch
		}
	}()
	go func() { // sender
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			ch <- i
		}
	}()
	wg.Wait()
}

// 502 ns/op
func BenchmarkChannels2(b *testing.B) {
	var wg sync.WaitGroup
	wg.Add(3)

	ch1 := make(chan int)
	ch2 := make(chan int)
	go func() { // sender
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			ch1 <- i
		}
	}()
	go func() { // proxyer
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			ch2 <- <-ch1
		}
	}()
	go func() { // receiver
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			<-ch2
		}
	}()

	wg.Wait()
}

func TestPanicRecoveryMutexCausesDeadlock(t *testing.T) {
	// Panic when a mutex is locked and unlocked this way can
	// cause a deadlock.
	var mu sync.Mutex

	for i := 0; i < 2; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("recovered from panic:", r)
				}
			}()
			mu.Lock()
			panic("panic in critical section")
			mu.Unlock()
		}()
	}
}

func TestPanicRecoveryMutexDeferDoesNotCauseDeadlock(t *testing.T) {
	// If the mutex unlock is deferred it happens even in a panic.
	var mu sync.Mutex

	for i := 0; i < 2; i++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Println("recovered from panic:", r)
				}
			}()
			mu.Lock()
			defer mu.Unlock()
			panic("panic in critical section")
		}()
	}
}

func TestSyncPool1(t *testing.T) {
	var idSequence uint64
	type resource struct {
		Id uint64
	}
	pool := &sync.Pool{
		New: func() interface{} {
			idSequence++
			return &resource{
				Id: idSequence,
			}
		},
	}

	fmt.Println(pool.Get()) // Instantiate instance 1
	instance := pool.Get()  // Instantiate instance 2
	fmt.Println(instance)
	pool.Put(instance)      // Return instance 2
	fmt.Println(pool.Get()) // Get instance 2
}

func TestSelectSliceOfChannels_Or(t *testing.T) {
	N := 2

	var or func(chs ...<-chan struct{}) <-chan struct{}
	or = func(chs ...<-chan struct{}) <-chan struct{} {
		switch len(chs) {
		case 0:
			return nil
		case 1:
			return chs[0]
		}

		orDone := make(chan struct{})
		go func() {
			switch len(chs) {
			case 2:
				select {
				case orDone <- <-chs[0]:
					fmt.Println("got 1")
				case orDone <- <-chs[1]:
					fmt.Println("got 2")
				}
			default:
				select {
				case orDone <- <-chs[0]:
					fmt.Println("got 3")
				case orDone <- <-chs[1]:
					fmt.Println("got 4")
				case orDone <- <-or(append(chs[2:], orDone)...):
					fmt.Println("got 5")
				}
			}
		}()
		return orDone
	}

	asyncWork := func() <-chan struct{} {
		doneCh := make(chan struct{})
		go func() {
			time.Sleep(time.Duration(rand.Intn(5000)) * time.Millisecond)
			doneCh <- struct{}{}
		}()
		return doneCh
	}

	workerDone := make([]<-chan struct{}, N)
	for i := 0; i < N; i++ {
		workerDone[i] = asyncWork()
	}

	select {
	case <-or(workerDone...):
		fmt.Println("a worker is done")
	}
}

func TestParallelErrorHandling(t *testing.T) {
	type Result struct {
		URL       string
		Error     error
		Response  *http.Response
		TimeSpent time.Duration
	}

	checkStatus := func(timeoutCh <-chan struct{}, urls ...string) <-chan Result {
		results := make(chan Result)
		var wg sync.WaitGroup

		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			<-timeoutCh
			cancel()
		}()

		wg.Add(len(urls))
		for _, url := range urls {
			go func(url string) {
				defer wg.Done()
				t0 := time.Now()

				req, _ := http.NewRequest("GET", url, nil)
				req = req.WithContext(ctx)

				resp, err := http.DefaultClient.Do(req)
				results <- Result{
					URL:       url,
					Error:     err,
					Response:  resp,
					TimeSpent: time.Since(t0),
				}
			}(url)
		}

		go func() {
			wg.Wait()
			close(results)
		}()

		return results
	}

	timeoutCh := make(chan struct{})
	defer close(timeoutCh)
	go func() {
		<-time.After(10 * time.Second)
		timeoutCh <- struct{}{}
	}()

	urls := []string{"http://www.google.com/", "https://vg.no/", "http://www.google.com/", "https://vg.no/", "http://www.google.com/", "httdps://vgfdsfds.no/"}
	for result := range checkStatus(timeoutCh, urls...) {
		fmt.Println("got result ", result.URL, "after", result.TimeSpent)
		if result.Error != nil {
			fmt.Println("error: ", result.Error)
			continue
		}
		fmt.Println("resp", result.Response.Status)
	}
}
