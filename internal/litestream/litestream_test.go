package litestream

import (
	"strings"
	"testing"
)

const sample = `# HELP go_goroutines Number of goroutines.
# TYPE go_goroutines gauge
go_goroutines 9
# HELP litestream_sync_error_count Sync errors
# TYPE litestream_sync_error_count counter
litestream_sync_error_count{db="/data/wifispot.db"} 2
litestream_db_size{db="/data/wifispot.db"} 16384
`

func TestFilterKeepsOnlyLitestream(t *testing.T) {
	out := string(Filter([]byte(sample)))
	if strings.Contains(out, "go_goroutines") {
		t.Errorf("go_ family leaked: %s", out)
	}
	for _, want := range []string{"# HELP litestream_sync_error_count", "# TYPE litestream_sync_error_count counter", `litestream_db_size{db="/data/wifispot.db"} 16384`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

func TestSyncErrors(t *testing.T) {
	if got := SyncErrors([]byte(sample)); got != 2 {
		t.Fatalf("want 2, got %v", got)
	}
	if got := SyncErrors(nil); got != 0 {
		t.Fatalf("empty: want 0, got %v", got)
	}
}
