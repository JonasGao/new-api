package operation_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

const LogMaxRangeDaysOptionKey = "log_setting.max_range_days"
const LogRequestBodyMaxBytesOptionKey = "log_setting.request_body_log_max_bytes"

// LogSetting limits how far regular users can reach into usage logs.
type LogSetting struct {
	// UserExportEnabled lets regular users export their own usage logs.
	// Administrators can always export.
	UserExportEnabled bool `json:"user_export_enabled"`
	// MaxRangeDays is the longest time range a user can search in their logs
	// and dashboard, and the longest range any export can cover.
	MaxRangeDays int `json:"max_range_days"`
	// RequestBodyLogEnabled stores a truncated copy of the client's original
	// request body on chat-class relay usage logs. The copy is visible to
	// administrators only.
	RequestBodyLogEnabled bool `json:"request_body_log_enabled"`
	// RequestBodyLogMaxBytes is the per-request truncation limit for stored
	// request bodies, in bytes.
	RequestBodyLogMaxBytes int `json:"request_body_log_max_bytes"`
}

var logSetting = LogSetting{
	UserExportEnabled:      true,
	MaxRangeDays:           365,
	RequestBodyLogEnabled:  false,
	RequestBodyLogMaxBytes: 65536,
}

func init() {
	config.GlobalConfig.Register("log_setting", &logSetting)
}

func GetLogSetting() *LogSetting {
	return &logSetting
}

func (s *LogSetting) MaxRangeSeconds() int64 {
	return int64(max(s.MaxRangeDays, 1)) * 24 * 60 * 60
}

func ValidateLogMaxRangeDays(value string) error {
	days, err := strconv.Atoi(value)
	if err != nil || days < 1 {
		return common.NewMessage("The longest log time range must be at least 1 day")
	}
	return nil
}

func ValidateLogMaxRequestBodyBytes(value string) error {
	maxBytes, err := strconv.Atoi(value)
	if err != nil || maxBytes < 1024 || maxBytes > 16*1024*1024 {
		return common.NewMessage("The request body storage limit must be between 1024 and 16777216 bytes")
	}
	return nil
}
