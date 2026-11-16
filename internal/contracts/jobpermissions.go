package contracts

// JobPermissionReporter is the optional read model for permission-downgrade
// awareness (2026-11 audit RG-09): running background jobs that were spawned
// under an older permission/sandbox identity and therefore keep the old OS
// confinement until they finish (OS constraints are applied at spawn time and
// are not retroactive). Consumers type-assert the KeyJobs service; the base
// JobService interface is unchanged.
type JobPermissionReporter interface {
	// StalePermissionJobs returns the running jobs still following an older
	// permission identity, scoped to the current workspace root. The slice is
	// a copy the caller may retain. Empty when nothing is stale.
	StalePermissionJobs() []BackgroundJob
}
