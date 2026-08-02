package automation

import state "github.com/xymorphic/morph/internal/state/core"

const (
	JobIDPrefix = state.AutomationJobIDPrefix
	RunIDPrefix = state.AutomationRunIDPrefix
)

type (
	ScheduleKind = state.AutomationScheduleKind
	PayloadKind  = state.AutomationPayloadKind
	DeliveryMode = state.AutomationDeliveryMode

	RunStatus      = state.AutomationRunStatus
	DeliveryStatus = state.AutomationDeliveryStatus

	Schedule = state.AutomationSchedule
	Payload  = state.AutomationPayload
	Delivery = state.AutomationDelivery

	JobState                = state.AutomationJobState
	AuthorizationProvenance = state.AutomationAuthorizationProvenance
	Usage                   = state.AutomationUsage

	Job      = state.AutomationJob
	JobPatch = state.AutomationJobPatch
	JobQuery = state.AutomationJobQuery
	JobList  = state.AutomationJobResult

	Run            = state.AutomationRun
	RunPatch       = state.AutomationRunPatch
	RunQuery       = state.AutomationRunQuery
	RunList        = state.AutomationRunResult
	RunDeleteQuery = state.AutomationRunDeleteQuery

	Store = state.AutomationStore
)

const (
	ScheduleAt    = state.AutomationScheduleAt
	ScheduleEvery = state.AutomationScheduleEvery
	ScheduleCron  = state.AutomationScheduleCron

	PayloadPrompt      = state.AutomationPayloadPrompt
	PayloadSystemEvent = state.AutomationPayloadSystemEvent

	DeliveryNone    = state.AutomationDeliveryNone
	DeliveryLocal   = state.AutomationDeliveryLocal
	DeliveryOrigin  = state.AutomationDeliveryOrigin
	DeliveryGateway = state.AutomationDeliveryGateway
	DeliveryWebhook = state.AutomationDeliveryWebhook

	RunStatusRunning = state.AutomationRunStatusRunning
	RunStatusOK      = state.AutomationRunStatusOK
	RunStatusError   = state.AutomationRunStatusError
	RunStatusSkipped = state.AutomationRunStatusSkipped

	DeliveryStatusDelivered    = state.AutomationDeliveryStatusDelivered
	DeliveryStatusNotDelivered = state.AutomationDeliveryStatusNotDelivered
	DeliveryStatusNotRequested = state.AutomationDeliveryStatusNotRequested
	DeliveryStatusUnknown      = state.AutomationDeliveryStatusUnknown
)

var (
	ValidateJobID = state.ValidateAutomationJobID
	ValidateRunID = state.ValidateAutomationRunID
)
