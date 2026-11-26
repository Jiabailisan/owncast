package utils

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/schollz/sqlite3dump"
	log "github.com/sirupsen/logrus"
)

// Restore will attempt to restore the database using a specified backup file.
func Restore(backupFile string, databaseFile string) error {
	log.Printf("Restoring database backup %s to %s", backupFile, databaseFile)

	data, err := os.ReadFile(backupFile) // nolint
	if err != nil {
		return fmt.Errorf("unable to read backup file %s", err)
	}

	gz, err := gzip.NewReader(bytes.NewBuffer(data))
	if err != nil {
		return fmt.Errorf("unable to read backup file %s", err)
	}
	// Older Owncast backups can contain trailing bytes from an untruncated overwrite.
	gz.Multistream(false)
	defer gz.Close()

	var b bytes.Buffer
	if _, err := io.Copy(&b, gz); err != nil { // nolint
		return fmt.Errorf("unable to read backup file %s", err)
	}

	defer gz.Close()

	rawSQL := b.String()

	// nolint:gosec
	f, err := os.Create(databaseFile)
	if err != nil {
		return errors.New("unable to write restored database")
	}
	if err := f.Close(); err != nil {
		return errors.New("unable to write restored database")
	}

	// Create a new database by executing the raw SQL
	db, err := sql.Open("sqlite3", databaseFile)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(rawSQL); err != nil {
		return err
	}

	return nil
}

// Backup will backup the provided instance of the database to the specified file.
func Backup(db *sql.DB, backupFile string) {
	log.Traceln("Backing up database to", backupFile)

	backupDirectory := filepath.Dir(backupFile)

	if !DoesFileExists(backupDirectory) {
		err := os.MkdirAll(backupDirectory, 0o700)
		if err != nil {
			log.Errorln("unable to create backup directory. check permissions and ownership.", backupDirectory, err)
			return
		}
	}

	// Stage 1: Dump SQL to a temporary file so the SQLite connection is released
	// immediately before running CPU-intensive gzip compression.
	tmpSQL, err := os.CreateTemp(backupDirectory, ".owncastdb-*.sql.tmp")
	if err != nil {
		handleError(err)
		return
	}
	tmpSQLPath := tmpSQL.Name()
	defer func() { _ = os.Remove(tmpSQLPath) }()
	if err := tmpSQL.Chmod(0o600); err != nil {
		_ = tmpSQL.Close()
		handleError(err)
		return
	}
	sqlOut := bufio.NewWriter(tmpSQL)
	if err := sqlite3dump.DumpDB(db, sqlOut); err != nil {
		_ = tmpSQL.Close()
		handleError(err)
		return
	}
	if err := sqlOut.Flush(); err != nil {
		_ = tmpSQL.Close()
		handleError(err)
		return
	}
	if _, err := tmpSQL.Seek(0, io.SeekStart); err != nil {
		_ = tmpSQL.Close()
		handleError(err)
		return
	}

	// Stage 2: Stream-compress the staged SQL file into an atomic backup file
	// without holding the DB connection or buffering the dump in memory.
	tmpFile, err := os.CreateTemp(backupDirectory, ".owncastdb-*.bak.tmp")
	if err != nil {
		_ = tmpSQL.Close()
		handleError(err)
		return
	}
	tmpPath := tmpFile.Name()
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpSQL.Close()
		_ = tmpFile.Close()
		handleError(err)
		return
	}

	gz, err := gzip.NewWriterLevel(tmpFile, gzip.BestCompression)
	if err != nil {
		_ = tmpSQL.Close()
		_ = tmpFile.Close()
		handleError(err)
		return
	}

	if _, err := io.Copy(gz, tmpSQL); err != nil {
		_ = tmpSQL.Close()
		_ = gz.Close()
		_ = tmpFile.Close()
		handleError(err)
		return
	}
	_ = tmpSQL.Close()
	if err := gz.Close(); err != nil {
		_ = tmpFile.Close()
		handleError(err)
		return
	}
	if err := tmpFile.Close(); err != nil {
		handleError(err)
		return
	}
	if err := os.Rename(tmpPath, backupFile); err != nil {
		handleError(err)
		return
	}
	cleanupTmp = false
}

func handleError(err error) {
	log.Errorln("unable to backup owncast database to file", err)
}
