package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type requestBodyLogFixture struct {
	db     *gorm.DB
	engine *gin.Engine
}

func newRequestBodyLogFixture(t *testing.T, handler gin.HandlerFunc) *requestBodyLogFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)

	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedisEnabled := common.RedisEnabled
	previousMainType := common.MainDatabaseType()
	previousLogType := common.LogDatabaseType()
	previousConsumeEnabled := common.LogConsumeEnabled
	previousSetting := *operation_setting.GetLogSetting()

	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, database.AutoMigrate(&model.User{}, &model.Log{}))
	model.DB, model.LOG_DB = database, database
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.LogConsumeEnabled = true

	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled = previousRedisEnabled
		common.SetDatabaseTypes(previousMainType, previousLogType)
		common.LogConsumeEnabled = previousConsumeEnabled
		*operation_setting.GetLogSetting() = previousSetting
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, database.Create(&model.User{Id: 7, Username: "log-owner", Group: "default"}).Error)

	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("id", 7)
		c.Set("username", "log-owner")
		c.Set("token_name", "test-token")
		c.Set("token_id", 11)
		c.Set("original_model", "gpt-test")
		c.Set("group", "default")
	})
	engine.Use(RelayRequestBodyLog())
	engine.POST("/v1/chat/completions", handler)

	return &requestBodyLogFixture{db: database, engine: engine}
}

func (f *requestBodyLogFixture) post(t *testing.T, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	f.engine.ServeHTTP(recorder, request)
	return recorder
}

func (f *requestBodyLogFixture) onlyLog(t *testing.T) model.Log {
	t.Helper()
	var logs []model.Log
	require.NoError(t, f.db.Order("id").Find(&logs).Error)
	require.Len(t, logs, 1)
	return logs[0]
}

func enableRequestBodyLog(maxBytes int) {
	setting := operation_setting.GetLogSetting()
	setting.RequestBodyLogEnabled = true
	setting.RequestBodyLogMaxBytes = maxBytes
}

func recordConsumeHandler(status int) gin.HandlerFunc {
	return func(c *gin.Context) {
		model.RecordConsumeLog(c, c.GetInt("id"), model.RecordConsumeLogParams{
			ModelName: c.GetString("original_model"),
			TokenName: c.GetString("token_name"),
			TokenId:   c.GetInt("token_id"),
			Group:     c.GetString("group"),
			Other:     model.NewLogOther(),
		})
		c.Status(status)
	}
}

func TestRelayRequestBodyLogCapture(t *testing.T) {
	t.Run("records_body_when_enabled", func(t *testing.T) {
		body := []byte(`{"model":"gpt-test","messages":[{"role":"user","content":"你好世界"}]}`)
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))
		enableRequestBodyLog(65536)

		f.post(t, body)

		stored := f.onlyLog(t)
		assert.Equal(t, model.LogTypeConsume, stored.Type)
		assert.Equal(t, string(body), stored.RequestBody)
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		adminInfo, ok := other["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(len(body)), adminInfo["request_body_size"])
		assert.NotContains(t, adminInfo, "request_body_truncated")

		detail, err := model.GetLogRequestBody(stored.Id)
		require.NoError(t, err)
		assert.Equal(t, string(body), detail.RequestBody)
	})

	t.Run("records_row_without_body_when_disabled", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))

		f.post(t, []byte(`{"model":"gpt-test"}`))

		stored := f.onlyLog(t)
		assert.Equal(t, model.LogTypeConsume, stored.Type)
		assert.Empty(t, stored.RequestBody)
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		if adminInfo, ok := other["admin_info"].(map[string]any); ok {
			assert.NotContains(t, adminInfo, "request_body_size")
		}
	})

	t.Run("stores_no_body_or_markers_for_degenerate_limit", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))
		enableRequestBodyLog(0)

		f.post(t, []byte(`{"model":"gpt-test"}`))

		stored := f.onlyLog(t)
		assert.Empty(t, stored.RequestBody)
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		if adminInfo, ok := other["admin_info"].(map[string]any); ok {
			assert.NotContains(t, adminInfo, "request_body_size")
			assert.NotContains(t, adminInfo, "request_body_truncated")
		}
	})
}

func TestRelayRequestBodyLogTruncationAndUTF8(t *testing.T) {
	t.Run("truncates_on_rune_boundary", func(t *testing.T) {
		body := []byte(strings.Repeat("你好", 10))
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))
		enableRequestBodyLog(25)

		f.post(t, body)

		stored := f.onlyLog(t)
		assert.LessOrEqual(t, len(stored.RequestBody), 25)
		assert.True(t, utf8.ValidString(stored.RequestBody))
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		adminInfo, ok := other["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(len(body)), adminInfo["request_body_size"])
		assert.Equal(t, true, adminInfo["request_body_truncated"])
	})

	t.Run("sanitizes_invalid_utf8", func(t *testing.T) {
		body := []byte(`{"q":"` + "\xff\xfe" + `"}`)
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))
		enableRequestBodyLog(65536)

		f.post(t, body)

		stored := f.onlyLog(t)
		assert.True(t, utf8.ValidString(stored.RequestBody))
		assert.NotContains(t, stored.RequestBody, "\xff")
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		adminInfo, ok := other["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(len(body)), adminInfo["request_body_size"])
		assert.NotContains(t, adminInfo, "request_body_truncated")
	})
}

func TestRelayRequestBodyLogFallback(t *testing.T) {
	failHandler := func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyRelayFailReason, "no available channel")
		c.Status(http.StatusServiceUnavailable)
	}

	t.Run("fallback_row_on_preflight_failure", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, failHandler)
		enableRequestBodyLog(65536)

		f.post(t, []byte(`{"model":"gpt-test"}`))

		stored := f.onlyLog(t)
		assert.Equal(t, model.LogTypeError, stored.Type)
		assert.Equal(t, "Relay request failed before a usage log was recorded", stored.Content)
		assert.Equal(t, `{"model":"gpt-test"}`, stored.RequestBody)
		assert.Equal(t, "gpt-test", stored.ModelName)
		other, err := common.StrToMap(stored.Other)
		require.NoError(t, err)
		adminInfo, ok := other["admin_info"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "no available channel", adminInfo["fail_reason"])
		assert.Equal(t, float64(http.StatusServiceUnavailable), other["status_code"])
	})

	t.Run("no_fallback_on_success_status", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, func(c *gin.Context) { c.Status(http.StatusOK) })
		enableRequestBodyLog(65536)

		f.post(t, []byte(`{"model":"gpt-test"}`))

		var count int64
		require.NoError(t, f.db.Model(&model.Log{}).Count(&count).Error)
		assert.Zero(t, count)
	})

	t.Run("no_duplicate_when_row_recorded", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusBadRequest))
		enableRequestBodyLog(65536)

		f.post(t, []byte(`{"model":"gpt-test"}`))

		stored := f.onlyLog(t)
		assert.Equal(t, model.LogTypeConsume, stored.Type)
	})

	t.Run("disabled_by_default", func(t *testing.T) {
		f := newRequestBodyLogFixture(t, failHandler)

		f.post(t, []byte(`{"model":"gpt-test"}`))

		var count int64
		require.NoError(t, f.db.Model(&model.Log{}).Count(&count).Error)
		assert.Zero(t, count)
	})
}

func TestRelayRequestBodyLogProjections(t *testing.T) {
	f := newRequestBodyLogFixture(t, recordConsumeHandler(http.StatusOK))
	enableRequestBodyLog(65536)

	f.post(t, []byte(`{"model":"gpt-test"}`))
	stored := f.onlyLog(t)
	require.NotEmpty(t, stored.RequestBody)

	logs, total, err := model.GetAllLogs(model.LogFilter{}, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	assert.Empty(t, logs[0].RequestBody)

	userLogs, _, err := model.GetUserLogs(7, model.LogFilter{}, 0, 10)
	require.NoError(t, err)
	assert.Empty(t, userLogs[0].RequestBody)
	userOther, err := common.StrToMap(userLogs[0].Other)
	require.NoError(t, err)
	assert.NotContains(t, userOther, "admin_info")

	tokenLogs, err := model.GetLogByTokenId(11)
	require.NoError(t, err)
	require.Len(t, tokenLogs, 1)
	assert.Empty(t, tokenLogs[0].RequestBody)
}

func TestLogRequestBodyColumnMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var dialector gorm.Dialector
			switch dialect {
			case "sqlite":
				dialector = sqlite.Open(filepath.Join(t.TempDir(), "logs.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("test database DSN is not configured")
				}
				dialector = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("test database DSN is not configured")
				}
				dialector = postgres.Open(dsn)
			}
			db, err := gorm.Open(dialector, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { _ = sqlDB.Close() })

			require.NoError(t, db.AutoMigrate(&model.Log{}))
			require.NoError(t, db.AutoMigrate(&model.Log{}))
			require.True(t, db.Migrator().HasColumn(&model.Log{}, "request_body"))

			if dialect == "mysql" {
				columns, err := db.Migrator().ColumnTypes(&model.Log{})
				require.NoError(t, err)
				for _, column := range columns {
					if column.Name() == "request_body" {
						dataType, _ := column.ColumnType()
						assert.Equal(t, "longtext", strings.ToLower(dataType))
					}
				}
			}
		})
	}
}
