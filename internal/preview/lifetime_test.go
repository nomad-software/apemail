package preview

import (
	"net/http"
	"testing"
	"time"
)

// TestLifetime checks that a message is served no more once its time is
// up, while it is being fetched, and that Close afterwards is harmless.
func TestLifetime(t *testing.T) {
	before := lifetime
	lifetime = 50 * time.Millisecond
	t.Cleanup(func() { lifetime = before })

	// One that is up at once must not close a server not yet started.
	lifetime = time.Nanosecond
	early, err := Serve("<p>Early</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	early.Close()

	lifetime = 50 * time.Millisecond
	page, err := Serve("<p>Hello</p>", nil)
	if err != nil {
		t.Fatal(err)
	}
	stop := time.Now().Add(5 * time.Second)
	served := 0
	for {
		resp, err := http.Get(page.URL)
		if err != nil {
			break
		}
		resp.Body.Close()
		served++
		if time.Now().After(stop) {
			t.Fatal("the message is still served after its time")
		}
	}
	if served == 0 {
		t.Error("the message was never served")
	}
	page.Close()
}
