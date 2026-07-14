/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package common

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type testCase struct {
	input          []string
	expectedOutput string
}

func TestEscapeTableName(t *testing.T) {
	t.Parallel()
	cases := []testCase{
		{[]string{}, ""},
		{[]string{"alpha", "testchannel"}, "alpha__testchannel"},
		{[]string{"alpha", "test-channel"}, "alpha__test_dchannel"},
		{[]string{"alpha", "test-channel", "other.param"}, "alpha__test_dchannel__other_fparam"},
	}
	for _, c := range cases {
		require.Equal(t, c.expectedOutput, escapeForTableName(c.input...))
	}
}

func TestEscapeTableNameError(t *testing.T) {
	t.Parallel()
	cases := [][]string{
		{"alpha", "testchannel!"},
		{"alpha", "test-#channel"},
	}
	for _, c := range cases {
		require.Panics(t, func() { escapeForTableName(c...) })
	}
}

func TestNewTableNameCreator(t *testing.T) {
	t.Parallel()
	creator := NewTableNameCreator("default")
	require.NotNil(t, creator)
	require.NotNil(t, creator.formatterProvider)
}

func TestTableNameCreator_GetFormatter(t *testing.T) {
	t.Parallel()
	creator := NewTableNameCreator("default")

	t.Run("valid prefix", func(t *testing.T) {
		t.Parallel()
		formatter, err := creator.GetFormatter("test")
		require.NoError(t, err)
		require.NotNil(t, formatter)
		require.Equal(t, "test_", formatter.prefix)
	})

	t.Run("empty prefix uses default", func(t *testing.T) {
		t.Parallel()
		formatter, err := creator.GetFormatter("")
		require.NoError(t, err)
		require.NotNil(t, formatter)
		require.Equal(t, "default_", formatter.prefix)
	})

	t.Run("prefix too long", func(t *testing.T) {
		t.Parallel()
		longPrefix := strings.Repeat("a", 101)
		_, err := creator.GetFormatter(longPrefix)
		require.Error(t, err)
		require.Contains(t, err.Error(), "table prefix must be shorter than 100 characters")
	})

	t.Run("invalid characters in prefix", func(t *testing.T) {
		t.Parallel()
		_, err := creator.GetFormatter("test-prefix")
		require.Error(t, err)
		require.Contains(t, err.Error(), "illegal character in table prefix")
	})

	t.Run("prefix with numbers is invalid", func(t *testing.T) {
		t.Parallel()
		_, err := creator.GetFormatter("test123")
		require.Error(t, err)
		require.Contains(t, err.Error(), "illegal character in table prefix")
	})

	t.Run("prefix with special chars is invalid", func(t *testing.T) {
		t.Parallel()
		_, err := creator.GetFormatter("test@prefix")
		require.Error(t, err)
		require.Contains(t, err.Error(), "illegal character in table prefix")
	})

	t.Run("cached formatter", func(t *testing.T) {
		t.Parallel()
		formatter1, err := creator.GetFormatter("cached")
		require.NoError(t, err)
		formatter2, err := creator.GetFormatter("cached")
		require.NoError(t, err)
		require.Equal(t, formatter1, formatter2)
	})
}

func TestTableNameCreator_MustGetTableName(t *testing.T) {
	t.Parallel()
	creator := NewTableNameCreator("fsc")

	t.Run("valid table name", func(t *testing.T) {
		t.Parallel()
		name := creator.MustGetTableName("test", "users")
		require.Equal(t, "test_users", name)
	})

	t.Run("with params", func(t *testing.T) {
		t.Parallel()
		name := creator.MustGetTableName("test", "users", "alpha", "beta")
		require.Equal(t, "test_alpha__beta_users", name)
	})

	t.Run("panics on invalid prefix", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			creator.MustGetTableName("invalid-prefix", "users")
		})
	})

	t.Run("panics on invalid name", func(t *testing.T) {
		t.Parallel()
		require.Panics(t, func() {
			creator.MustGetTableName("test", "invalid-name")
		})
	})
}

func TestTableNameCreator_CreateTableName(t *testing.T) {
	t.Parallel()
	creator := NewTableNameCreator("default")

	t.Run("valid table name", func(t *testing.T) {
		t.Parallel()
		name, err := creator.CreateTableName("test", "users")
		require.NoError(t, err)
		require.Equal(t, "test_users", name)
	})

	t.Run("empty prefix uses default", func(t *testing.T) {
		t.Parallel()
		name, err := creator.CreateTableName("", "users")
		require.NoError(t, err)
		require.Equal(t, "default_users", name)
	})

	t.Run("with single param", func(t *testing.T) {
		t.Parallel()
		name, err := creator.CreateTableName("test", "users", "alpha")
		require.NoError(t, err)
		require.Equal(t, "test_alpha_users", name)
	})

	t.Run("with multiple params", func(t *testing.T) {
		t.Parallel()
		name, err := creator.CreateTableName("test", "users", "alpha", "beta", "gamma")
		require.NoError(t, err)
		require.Equal(t, "test_alpha__beta__gamma_users", name)
	})

	t.Run("params with special chars", func(t *testing.T) {
		t.Parallel()
		name, err := creator.CreateTableName("test", "users", "test-channel", "other.param")
		require.NoError(t, err)
		require.Equal(t, "test_test_dchannel__other_fparam_users", name)
	})

	t.Run("invalid prefix", func(t *testing.T) {
		t.Parallel()
		_, err := creator.CreateTableName("invalid-prefix", "users")
		require.Error(t, err)
	})

	t.Run("invalid name", func(t *testing.T) {
		t.Parallel()
		_, err := creator.CreateTableName("test", "invalid-name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid table name")
	})

	t.Run("name with numbers is invalid", func(t *testing.T) {
		t.Parallel()
		_, err := creator.CreateTableName("test", "users123")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid table name")
	})
}

func TestTableNameFormatter_Format(t *testing.T) {
	t.Parallel()

	t.Run("simple name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users")
		require.NoError(t, err)
		require.Equal(t, "test_users", name)
	})

	t.Run("name with underscore", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("user_data")
		require.NoError(t, err)
		require.Equal(t, "test_user_data", name)
	})

	t.Run("with single param", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users", "alpha")
		require.NoError(t, err)
		require.Equal(t, "test_alpha_users", name)
	})

	t.Run("with multiple params", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users", "alpha", "beta")
		require.NoError(t, err)
		require.Equal(t, "test_alpha__beta_users", name)
	})

	t.Run("params with dash", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users", "test-channel")
		require.NoError(t, err)
		require.Equal(t, "test_test_dchannel_users", name)
	})

	t.Run("params with dot", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users", "other.param")
		require.NoError(t, err)
		require.Equal(t, "test_other_fparam_users", name)
	})

	t.Run("params with underscore", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name, err := formatter.Format("users", "test_param")
		require.NoError(t, err)
		require.Equal(t, "test_test__param_users", name)
	})

	t.Run("invalid name with dash", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		_, err := formatter.Format("invalid-name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid table name")
	})

	t.Run("invalid name with number", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		_, err := formatter.Format("users123")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid table name")
	})

	t.Run("empty prefix", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "",
			r:      validName,
		}
		name, err := formatter.Format("users")
		require.NoError(t, err)
		require.Equal(t, "users", name)
	})
}

func TestTableNameFormatter_MustFormat(t *testing.T) {
	t.Parallel()

	t.Run("valid name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		name := formatter.MustFormat("users")
		require.Equal(t, "test_users", name)
	})

	t.Run("panics on invalid name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{
			prefix: "test_",
			r:      validName,
		}
		require.Panics(t, func() {
			formatter.MustFormat("invalid-name")
		})
	})
}

func TestTableNameFormatter_FormatWithoutPrefix(t *testing.T) {
	t.Parallel()

	t.Run("simple name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{prefix: "test_", r: validName}
		name, err := formatter.FormatWithoutPrefix("users")
		require.NoError(t, err)
		require.Equal(t, "users", name)
	})

	t.Run("with params", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{prefix: "test_", r: validName}
		name, err := formatter.FormatWithoutPrefix("users", "alpha", "beta")
		require.NoError(t, err)
		require.Equal(t, "alpha__beta_users", name)
	})

	t.Run("invalid name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{prefix: "test_", r: validName}
		_, err := formatter.FormatWithoutPrefix("invalid-name")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid table name")
	})
}

func TestTableNameFormatter_MustFormatWithoutPrefix(t *testing.T) {
	t.Parallel()

	t.Run("valid name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{prefix: "test_", r: validName}
		name := formatter.MustFormatWithoutPrefix("users")
		require.Equal(t, "users", name)
	})

	t.Run("panics on invalid name", func(t *testing.T) {
		t.Parallel()
		formatter := &tableNameFormatter{prefix: "test_", r: validName}
		require.Panics(t, func() {
			formatter.MustFormatWithoutPrefix("invalid-name")
		})
	})
}

func TestReplacer(t *testing.T) {
	t.Parallel()

	t.Run("underscore replacer", func(t *testing.T) {
		t.Parallel()
		r := newReplacer("_", "__")
		result := r.Escape("test_value")
		require.Equal(t, "test__value", result)
	})

	t.Run("dash replacer", func(t *testing.T) {
		t.Parallel()
		r := newReplacer("-", "_d")
		result := r.Escape("test-value")
		require.Equal(t, "test_dvalue", result)
	})

	t.Run("dot replacer", func(t *testing.T) {
		t.Parallel()
		r := newReplacer("\\.", "_f")
		result := r.Escape("test.value")
		require.Equal(t, "test_fvalue", result)
	})

	t.Run("no match", func(t *testing.T) {
		t.Parallel()
		r := newReplacer("-", "_d")
		result := r.Escape("testvalue")
		require.Equal(t, "testvalue", result)
	})

	t.Run("multiple matches", func(t *testing.T) {
		t.Parallel()
		r := newReplacer("-", "_d")
		result := r.Escape("test-value-name")
		require.Equal(t, "test_dvalue_dname", result)
	})
}

func TestEscapeForTableName_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("single param", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("alpha")
		require.Equal(t, "alpha", result)
	})

	t.Run("param with all special chars", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("test-channel.param_value")
		require.Equal(t, "test_dchannel_fparam__value", result)
	})

	t.Run("multiple params with special chars", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("test-channel", "other.param", "value_name")
		require.Equal(t, "test_dchannel__other_fparam__value__name", result)
	})

	t.Run("param with consecutive underscores", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("test__value")
		require.Equal(t, "test____value", result)
	})

	t.Run("param with consecutive dashes", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("test--value")
		require.Equal(t, "test_d_dvalue", result)
	})

	t.Run("param with consecutive dots", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("test..value")
		require.Equal(t, "test_f_fvalue", result)
	})

	t.Run("uppercase letters", func(t *testing.T) {
		t.Parallel()
		result := escapeForTableName("TestValue")
		require.Equal(t, "TestValue", result)
	})
}

func TestTableNameCreator_DefaultPrefix(t *testing.T) {
	t.Parallel()

	t.Run("empty default prefix", func(t *testing.T) {
		t.Parallel()
		creator := NewTableNameCreator("")
		formatter, err := creator.GetFormatter("")
		require.NoError(t, err)
		require.Empty(t, formatter.prefix)
	})

	t.Run("non-empty default prefix", func(t *testing.T) {
		t.Parallel()
		creator := NewTableNameCreator("mydefault")
		formatter, err := creator.GetFormatter("")
		require.NoError(t, err)
		require.Equal(t, "mydefault_", formatter.prefix)
	})

	t.Run("override default prefix", func(t *testing.T) {
		t.Parallel()
		creator := NewTableNameCreator("mydefault")
		formatter, err := creator.GetFormatter("custom")
		require.NoError(t, err)
		require.Equal(t, "custom_", formatter.prefix)
	})
}

func TestTableNameCreator_CaseInsensitivePrefix(t *testing.T) {
	t.Parallel()
	creator := NewTableNameCreator("default")

	t.Run("uppercase prefix converted to lowercase", func(t *testing.T) {
		t.Parallel()
		formatter, err := creator.GetFormatter("TEST")
		require.NoError(t, err)
		require.Equal(t, "test_", formatter.prefix)
	})

	t.Run("mixed case prefix converted to lowercase", func(t *testing.T) {
		t.Parallel()
		formatter, err := creator.GetFormatter("TeSt")
		require.NoError(t, err)
		require.Equal(t, "test_", formatter.prefix)
	})
}

func TestValidNameRegex(t *testing.T) {
	t.Parallel()

	t.Run("valid names", func(t *testing.T) {
		t.Parallel()
		validNames := []string{
			"users",
			"user_data",
			"USER_DATA",
			"_private",
			"a",
			"A",
			"_",
		}
		for _, name := range validNames {
			require.True(t, validName.MatchString(name), "expected %s to be valid", name)
		}
	})

	t.Run("invalid names", func(t *testing.T) {
		t.Parallel()
		invalidNames := []string{
			"users123",
			"user-data",
			"user.data",
			"user data",
			"123users",
			"user@data",
			"",
		}
		for _, name := range invalidNames {
			require.False(t, validName.MatchString(name), "expected %s to be invalid", name)
		}
	})
}
