package activity

const (
	SourceClient       = "client"
	SourceDashboard    = "dashboard"
	SourceServer       = "server"
	SourceBridge       = "bridge"
	SourceOrchestrator = "orchestrator"
	SourceScheduler    = "scheduler"
	SourceMCP          = "mcp"
)

// IsDashboardAgentActivity is the one predicate for "does this event belong in the
// dashboard's per-agent views". The live broadcast and the persisted-log rebuild both
// call it, so a source counted while the server runs is the same source restored after
// a restart; two separately spelled predicates drifted once and dropped scheduled
// actions from the per-agent summaries after any restart.
func IsDashboardAgentActivity(evt Event) bool {
	return evt.Source == SourceClient || evt.Source == SourceScheduler
}
