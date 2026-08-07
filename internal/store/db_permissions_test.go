//go:build unix

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenPathCreatesOwnerOnlyDataPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".same", "data")
	dbPath := filepath.Join(dataDir, "vault.db")

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertPathMode(t, dataDir, 0o700)
	assertPathMode(t, dbPath, 0o600)
}

func TestOpenPathUsesLiteralQuestionMarkInFilesystemPath(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	dbPath := filepath.Join(dataDir, "vault?literal.db")
	truncatedPath := filepath.Join(dataDir, "vault")

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertOpenedDatabasePath(t, db, dbPath)
	assertSQLiteDSNSettings(t, db)
	if _, err := db.Conn().Exec(`CREATE TABLE path_regression (value TEXT NOT NULL)`); err != nil {
		t.Fatalf("write requested database: %v", err)
	}
	if err := db.IntegrityCheck(); err != nil {
		t.Fatalf("requested database is not a working SQLite database: %v", err)
	}
	if _, err := os.Lstat(truncatedPath); !os.IsNotExist(err) {
		t.Fatalf("truncated sibling was created at %s: %v", truncatedPath, err)
	}
	assertPathMode(t, dbPath, 0o600)
}

func TestOpenPathUsesExplicitRelativeFilesystemPath(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	dbPath := filepath.Join("data", "vault.db")

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertOpenedDatabasePath(t, db, filepath.Join(workDir, dbPath))
	assertPathMode(t, filepath.Join(workDir, dbPath), 0o600)
}

func TestOpenPathUsesOtherURIReservedCharactersLiterally(t *testing.T) {
	for _, name := range []string{
		"vault#literal.db",
		"vault%literal.db",
		"vault%3Fliteral.db",
	} {
		t.Run(name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "data", name)
			db, err := OpenPath(dbPath)
			if err != nil {
				t.Fatalf("OpenPath: %v", err)
			}
			defer db.Close()

			assertOpenedDatabasePath(t, db, dbPath)
			assertPathMode(t, dbPath, 0o600)
		})
	}
}

func assertSQLiteDSNSettings(t *testing.T, db *DB) {
	t.Helper()
	var journalMode string
	if err := db.Conn().QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
	var synchronous, busyTimeout int
	if err := db.Conn().QueryRow(`PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatalf("read synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
	if err := db.Conn().QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}
}

func assertOpenedDatabasePath(t *testing.T, db *DB, want string) {
	t.Helper()
	var sequence int
	var name, got string
	if err := db.Conn().QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &got); err != nil {
		t.Fatalf("read opened database path: %v", err)
	}
	want, err := filepath.Abs(want)
	if err != nil {
		t.Fatalf("resolve expected database path: %v", err)
	}
	got, err = filepath.Abs(got)
	if err != nil {
		t.Fatalf("resolve opened database path: %v", err)
	}
	if got != want {
		t.Fatalf("opened database path = %q, want %q", got, want)
	}
}

func TestOpenPathHardensExistingPermissiveDataPaths(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".same", "data")
	dbPath := filepath.Join(dataDir, "vault.db")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("create permissive data dir: %v", err)
	}
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatalf("set permissive data dir mode: %v", err)
	}
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatalf("create permissive database: %v", err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("set permissive database mode: %v", err)
	}

	db, err := OpenPath(dbPath)
	if err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	defer db.Close()

	assertPathMode(t, dataDir, 0o700)
	assertPathMode(t, dbPath, 0o600)
}

func TestOpenPathRejectsSymlinkedDataDirectory(t *testing.T) {
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	if err := os.Mkdir(targetDir, 0o755); err != nil {
		t.Fatalf("create target dir: %v", err)
	}
	dataDir := filepath.Join(root, "data")
	if err := os.Symlink(targetDir, dataDir); err != nil {
		t.Fatalf("symlink data dir: %v", err)
	}

	db, err := OpenPath(filepath.Join(dataDir, "vault.db"))
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath succeeded through symlinked data directory")
	}
	assertPathMode(t, targetDir, 0o755)
}

func TestOpenPathRejectsSymlinkedExistingDatabase(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	targetPath := filepath.Join(t.TempDir(), "target.db")
	if err := os.WriteFile(targetPath, nil, 0o644); err != nil {
		t.Fatalf("create target database: %v", err)
	}
	dbPath := filepath.Join(dataDir, "vault.db")
	if err := os.Symlink(targetPath, dbPath); err != nil {
		t.Fatalf("symlink database: %v", err)
	}

	db, err := OpenPath(dbPath)
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath succeeded with symlinked existing database")
	}
	assertPathMode(t, targetPath, 0o644)
}

func TestOpenPathRejectsMemoryDSNWithoutChangingWorkingDirectory(t *testing.T) {
	workDir := t.TempDir()
	if err := os.Chmod(workDir, 0o755); err != nil {
		t.Fatalf("set working directory mode: %v", err)
	}
	t.Chdir(workDir)

	db, err := OpenPath(":memory:")
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath accepted :memory:; use OpenMemory instead")
	}
	assertPathMode(t, workDir, 0o755)
	if _, statErr := os.Lstat(filepath.Join(workDir, ":memory:")); !os.IsNotExist(statErr) {
		t.Fatalf("literal :memory: path was created: %v", statErr)
	}
}

func TestOpenPathRejectsFileURIWithoutCreatingLiteralPath(t *testing.T) {
	workDir := t.TempDir()
	if err := os.Chmod(workDir, 0o755); err != nil {
		t.Fatalf("set working directory mode: %v", err)
	}
	t.Chdir(workDir)

	db, err := OpenPath("file:memorydb?mode=memory&cache=shared")
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath accepted file: URI")
	}
	assertPathMode(t, workDir, 0o755)
	if _, statErr := os.Lstat(filepath.Join(workDir, "file:memorydb")); !os.IsNotExist(statErr) {
		t.Fatalf("literal file: path was created: %v", statErr)
	}
}

func TestOpenPathRejectsBareRelativeFilenameWithoutChangingWorkingDirectory(t *testing.T) {
	workDir := t.TempDir()
	if err := os.Chmod(workDir, 0o755); err != nil {
		t.Fatalf("set working directory mode: %v", err)
	}
	t.Chdir(workDir)

	db, err := OpenPath("vault.db")
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath accepted a bare relative filename")
	}
	assertPathMode(t, workDir, 0o755)
	if _, statErr := os.Lstat(filepath.Join(workDir, "vault.db")); !os.IsNotExist(statErr) {
		t.Fatalf("bare relative database was created: %v", statErr)
	}
}

func TestOpenPathHardensExistingDatabaseBeforeSQLiteReadsIt(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	dbPath := filepath.Join(dataDir, "vault.db")
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o644); err != nil {
		t.Fatalf("create invalid database: %v", err)
	}
	if err := os.Chmod(dbPath, 0o644); err != nil {
		t.Fatalf("set permissive database mode: %v", err)
	}

	db, err := OpenPath(dbPath)
	if db != nil {
		_ = db.Close()
	}
	if err == nil {
		t.Fatal("OpenPath unexpectedly accepted invalid database")
	}
	assertPathMode(t, dbPath, 0o600)
}

func assertPathMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode for %s = %#o, want %#o", path, got, want)
	}
}
