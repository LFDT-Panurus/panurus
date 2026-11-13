/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package token_test

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/actions"
	_ "github.com/LFDT-Panurus/panurus/token/core/fabtoken/protos-go/v1/pp"
	_ "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/actions"
	_ "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/math"
	_ "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/protos-go/v1/pp"
	_ "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1"
	_ "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/pp"
	_ "github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	_ "github.com/LFDT-Panurus/panurus/token/services/identity/idemix/crypto/protos-go/config"
	_ "github.com/LFDT-Panurus/panurus/token/services/identity/x509/crypto/protos-go/v1/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// TestProtoRegistryNamespace checks that every Panurus proto on disk registers under its
// repo-relative path and the panurus package, so it does not clash with other modules in the global registry.
func TestProtoRegistryNamespace(t *testing.T) {
	const goPackagePrefix = "github.com/LFDT-Panurus/panurus/"

	var onDisk []string
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".claude" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".proto") {
			rel, err := filepath.Rel("..", path)
			if err != nil {
				return err
			}
			onDisk = append(onDisk, filepath.ToSlash(rel))
		}

		return nil
	})
	require.NoError(t, err)
	require.NotEmpty(t, onDisk)

	var registered []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		opts, ok := fd.Options().(*descriptorpb.FileOptions)
		if !ok || !strings.HasPrefix(opts.GetGoPackage(), goPackagePrefix) {
			return true
		}
		registered = append(registered, fd.Path())
		assert.True(t, strings.HasPrefix(string(fd.Package()), "panurus."), "file %s has package %s, want panurus.*", fd.Path(), fd.Package())

		return true
	})
	assert.ElementsMatch(t, onDisk, registered, "registered proto paths must match the repo-relative .proto files")

	_, err = protoregistry.GlobalFiles.FindFileByPath("common.proto")
	assert.ErrorIs(t, err, protoregistry.NotFound, "bare file paths must not be registered")
}
