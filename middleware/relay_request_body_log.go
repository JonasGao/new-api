package middleware

import (
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
)

// RelayRequestBodyLog flags chat-class relay routes for request-body capture
// and records a fallback error row when a request fails before any usage-log
// row exists (for example: no available channel, invalid request body).
//
// It MUST be registered before Distribute: distributor aborts skip every
// later handler, and this middleware only observes the abort after c.Next()
// returns. Registering it after Distribute silently disables the fallback.
func RelayRequestBodyLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !operation_setting.GetLogSetting().RequestBodyLogEnabled {
			c.Next()
			return
		}
		common.SetContextKey(c, constant.ContextKeyLogRequestBodyCapture, true)
		c.Next()
		if common.GetContextKeyBool(c, constant.ContextKeyRelayLogRecorded) {
			return
		}
		if c.Writer.Status() < http.StatusBadRequest {
			return
		}
		model.RecordRelayFallbackErrorLog(c)
	}
}
