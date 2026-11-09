package protocol

import (
	"github.com/Wei-Shaw/sub2api/internal/custombridge"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type Config struct {
	DSN             string
	Mode            string
	ProviderURL     string
	IntentPath      string
	ContinuePath    string
	Acknowledgement string
	Reconcile       bool
	Command         service.UsageBillingCommand
}

// Intent 是演练夹具的持久化发送意图，不是 Core 的生产发送防重实现。
type Intent struct {
	State         string               `json:"state"`
	SenderPID     int                  `json:"sender_pid"`
	CoreRequestID string               `json:"core_request_id"`
	InternalKeyID int64                `json:"internal_key_id"`
	Request       custombridge.Request `json:"request"`
}

type Event struct {
	Stage      string                `json:"stage"`
	PID        int                   `json:"pid"`
	BackendPID int                   `json:"backend_pid,omitempty"`
	State      string                `json:"state,omitempty"`
	Applied    bool                  `json:"applied"`
	Receipt    *custombridge.Receipt `json:"receipt,omitempty"`
	Intent     *Intent               `json:"intent,omitempty"`
}
