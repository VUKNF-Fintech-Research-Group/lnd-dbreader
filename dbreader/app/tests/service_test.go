// -----------------------------------------------------------
//  [*] tests — the service binary (service_test.go)
//
//  The dbreader process itself, configured the way compose
//  configures it — environment in, channel.db copied to
//  /tmp, initial sync, then the ticker — and stopped the way
//  docker stop stops it. Pins what main.go promises: a full
//  sync of the v0.21.4-written graph lands the expected rows
//  and cleans up its copy, a failed sync is logged while the
//  loop keeps running, an unreachable MySQL is fatal at
//  start (the restart policy is the retry), and SIGTERM ends
//  it with exit code 0. The tests run one at a time — the
//  binary's copy path is fixed.
// -----------------------------------------------------------


package tests

import (
	// Standard library
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)








// The copy path main.go hard-codes as tempDatabasePath
const serviceCopyPath = "/tmp/channel_copy.db"








// -----------------------------------------------------------
// serviceEnv
// -----------------------------------------------------------
//
// The environment compose would give the service, aimed at
// the test's own database and the given channel.db, with an
// hour between syncs so no tick fires during a test.
//
// Used by:
//   - the tests below
// -----------------------------------------------------------

func serviceEnv(cfg mysqlConfig, database, lndDBPath string) []string {
	return []string{
		"MYSQL_HOST=" + cfg.host,
		"MYSQL_PORT=" + cfg.port,
		"MYSQL_USER=" + cfg.user,
		"MYSQL_PASSWORD=" + cfg.password,
		"MYSQL_DATABASE=" + database,
		"LND_DB_PATH=" + lndDBPath,
		"SYNC_INTERVAL_MINUTES=60",
	}
}








// -----------------------------------------------------------
// TestServiceSyncsAndStopsCleanly
// -----------------------------------------------------------
//
// One full service run on the graph LND v0.21.4 writes: the
// initial sync reports success, the rows equal the golden
// ones with the DNS delta, the private copy is deleted
// after the sync, and SIGTERM ends the process gracefully
// with exit code 0.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestServiceSyncsAndStopsCleanly(t *testing.T) {
	conn, cfg, database := newTestDatabase(t)

	svc := startService(t, serviceEnv(cfg, database, v0214Fixture(t))...)
	svc.waitFor(t, "✅ Initial sync completed successfully!", time.Minute)

	assertRowsEqual(t, goldenForV0214(t), dumpRows(t, conn))

	if _, err := os.Stat(serviceCopyPath); !os.IsNotExist(err) {
		t.Errorf("%s still there after the sync (stat: %v)", serviceCopyPath, err)
	}

	if code := svc.stop(t); code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, svc.log())
	}
	if !strings.Contains(svc.log(), "Shutdown signal received, exiting gracefully") {
		t.Errorf("no graceful-shutdown line:\n%s", svc.log())
	}
}








// -----------------------------------------------------------
// TestServiceKeepsRunningAfterAFailedSync
// -----------------------------------------------------------
//
// A sync that fails — here the channel.db is missing, as
// before the LND node first starts — is logged with the
// retry interval and the process stays up for the next
// tick; it still stops cleanly.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestServiceKeepsRunningAfterAFailedSync(t *testing.T) {
	_, cfg, database := newTestDatabase(t)

	svc := startService(t, serviceEnv(cfg, database, filepath.Join(t.TempDir(), "missing.db"))...)
	svc.waitFor(t, "ERROR during initial sync", time.Minute)
	svc.waitFor(t, "Will retry in 1h0m0s", 5*time.Second)

	select {
	case <-svc.exited:
		t.Fatalf("the service exited after a failed sync:\n%s", svc.log())
	case <-time.After(time.Second):
	}

	if code := svc.stop(t); code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, svc.log())
	}
}








// -----------------------------------------------------------
// TestServiceExitsWhenMySQLIsUnreachable
// -----------------------------------------------------------
//
// No MySQL at start is fatal — exit code 1 and the reason in
// the log — so the container's restart policy retries the
// whole service. Nothing listens on port 1 of the loopback,
// so the refusal is immediate and needs no test MySQL.
//
// Used by:
//   - go test (runTests.sh)
// -----------------------------------------------------------

func TestServiceExitsWhenMySQLIsUnreachable(t *testing.T) {
	cfg := mysqlConfig{host: "127.0.0.1", port: "1", user: "nobody", password: "nothing"}
	svc := startService(t, serviceEnv(cfg, "nowhere", filepath.Join(t.TempDir(), "missing.db"))...)

	select {
	case <-svc.exited:
	case <-time.After(30 * time.Second):
		t.Fatalf("the service kept running without MySQL:\n%s", svc.log())
	}

	// The output may still be draining
	time.Sleep(100 * time.Millisecond)
	if code := svc.cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("exit code = %d, want 1:\n%s", code, svc.log())
	}
	if !strings.Contains(svc.log(), "MySQL connection failed") {
		t.Errorf("no MySQL failure in the log:\n%s", svc.log())
	}
}
