package conc

import "testing"
import "context"
func TestRun_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	jobs := make(chan string)
	done := make(chan []string)

	go func() {
		done <- Run(ctx, jobs)
	}()

	jobs <- "a"
	cancel()
	got := <-done
	if len(got) < 1 {
		t.Fatalf("got %v", got)
	}
}