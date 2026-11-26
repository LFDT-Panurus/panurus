/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ownedskis

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func execCmd(t *testing.T, stdout io.Writer, args ...string) (string, error) {
	t.Helper()
	var errOut bytes.Buffer
	cmd := Cmd()
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(&errOut)
	err := cmd.Execute()

	return errOut.String(), err
}

func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// C10
func TestCmd_OutputStreamsSeparated(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)

	var stdout bytes.Buffer
	errOut, err := execCmd(t, &stdout, "--config", db.configPath)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte("alice"))+"\n", stdout.String())
	assert.Contains(t, errOut, "--- Statistics ---")
}

// C11
func TestCmd_OutputFileMatchesStdout(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	db.storeToken("tx2", fallbackType, []byte("bob"), true)
	out := filepath.Join(t.TempDir(), "skis.txt")

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath, "--output", out)
	require.NoError(t, err)

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, stdout.String(), string(content))
	assert.NotEmpty(t, content)

	fi, err := os.Stat(out)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
	assert.Equal(t, []string{"skis.txt"}, dirEntries(t, filepath.Dir(out)), "no temp file left")
}

// C12
func TestCmd_OutputFileOverwrites(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	out := filepath.Join(t.TempDir(), "skis.txt")
	require.NoError(t, os.WriteFile(out, []byte("old content that is longer than the new list\n"), 0o644))

	_, err := execCmd(t, &bytes.Buffer{}, "--config", db.configPath, "--output", out)
	require.NoError(t, err)

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte("alice"))+"\n", string(content))
}

// C13
func TestCmd_OutputIsDirectory(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	dir := t.TempDir()

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath, "--output", dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
	assert.Empty(t, stdout.String())
	assert.Empty(t, dirEntries(t, dir))
}

// C14
func TestCmd_NoPartialFileOnError(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	// Garbage idemix identity: extraction fails.
	db.storeToken("tx2", "idemix", []byte("garbage"), true)
	dir := t.TempDir()
	out := filepath.Join(dir, "skis.txt")
	require.NoError(t, os.WriteFile(out, []byte("previous\n"), 0o600))

	var stdout bytes.Buffer
	errOut, err := execCmd(t, &stdout, "--config", db.configPath, "--output", out)
	require.Error(t, err)
	assert.Empty(t, stdout.String(), "no partial list on stdout")
	assert.Contains(t, errOut, "owner type [idemix]")

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "previous\n", string(content), "existing file untouched")
	assert.Equal(t, []string{"skis.txt"}, dirEntries(t, dir), "no temp file left")
}

// C15
func TestCmd_StdoutWriteError(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	dir := t.TempDir()
	out := filepath.Join(dir, "skis.txt")

	_, err := execCmd(t, failingWriter{}, "--config", db.configPath, "--output", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write list to stdout")
	assert.Empty(t, dirEntries(t, dir), "file not written")
}

// C18
func TestCmd_EmptyDB(t *testing.T) {
	db := newTestDB(t)
	out := filepath.Join(t.TempDir(), "skis.txt")

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath, "--output", out)
	require.NoError(t, err)
	assert.Empty(t, stdout.String())

	content, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Empty(t, content)
}

// The temp file is removed and the target untouched when the rename fails.
func TestWriteFileAtomic_RenameFailure(t *testing.T) {
	dir := t.TempDir()
	// A non-empty directory as target makes the rename fail.
	target := filepath.Join(dir, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(target, "keep"), nil, 0o600))

	err := writeFileAtomic(target, []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rename temp file")
	assert.Equal(t, []string{"target"}, dirEntries(t, dir), "temp file removed")
}

// A missing target directory fails before any file is created.
func TestWriteFileAtomic_MissingDir(t *testing.T) {
	err := writeFileAtomic(filepath.Join(t.TempDir(), "nope", "skis.txt"), []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temp file")
}

// A file write that fails after stdout succeeded: stdout holds the full list, the command fails.
func TestCmd_FileWriteFailsAfterStdout(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	out := filepath.Join(t.TempDir(), "missing", "skis.txt")

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath, "--output", out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write list to ["+out+"]")
	assert.Equal(t, sha256Hex([]byte("alice"))+"\n", stdout.String())
}

// "-" is rejected instead of creating a file named "-".
func TestCmd_OutputDashRejected(t *testing.T) {
	db := newTestDB(t)
	t.Chdir(t.TempDir())

	var stdout bytes.Buffer
	_, err := execCmd(t, &stdout, "--config", db.configPath, "--output", "-")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--output - is not supported")
	assert.Empty(t, stdout.String())
	assert.Empty(t, dirEntries(t, "."))
}

// A symlink at --output is replaced by a regular file; its target is untouched.
func TestCmd_OutputSymlinkReplaced(t *testing.T) {
	db := newTestDB(t)
	db.storeToken("tx1", fallbackType, []byte("alice"), true)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.WriteFile(target, []byte("old\n"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	_, err := execCmd(t, &bytes.Buffer{}, "--config", db.configPath, "--output", link)
	require.NoError(t, err)

	fi, err := os.Lstat(link)
	require.NoError(t, err)
	assert.True(t, fi.Mode().IsRegular(), "symlink replaced by a regular file")
	content, err := os.ReadFile(link)
	require.NoError(t, err)
	assert.Equal(t, sha256Hex([]byte("alice"))+"\n", string(content))
	content, err = os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old\n", string(content), "symlink target untouched")
}
