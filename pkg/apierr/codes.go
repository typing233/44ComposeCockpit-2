package apierr

const (
	// Authentication / Authorization
	ErrUnauthorized  = "ERR_UNAUTHORIZED"
	ErrForbidden     = "ERR_FORBIDDEN"
	ErrTokenExpired  = "ERR_TOKEN_EXPIRED"
	ErrTokenInvalid  = "ERR_TOKEN_INVALID"

	// Validation
	ErrValidation       = "ERR_VALIDATION"
	ErrInvalidProjectID = "ERR_INVALID_PROJECT_ID"
	ErrInvalidService   = "ERR_INVALID_SERVICE"

	// Resource Not Found
	ErrProjectNotFound   = "ERR_PROJECT_NOT_FOUND"
	ErrServiceNotFound   = "ERR_SERVICE_NOT_FOUND"
	ErrUserNotFound      = "ERR_USER_NOT_FOUND"
	ErrOperationNotFound = "ERR_OPERATION_NOT_FOUND"

	// Conflict / State
	ErrPortConflict        = "ERR_PORT_CONFLICT"
	ErrNetworkConflict     = "ERR_NETWORK_CONFLICT"
	ErrVolumeConflict      = "ERR_VOLUME_CONFLICT"
	ErrProjectLocked       = "ERR_PROJECT_LOCKED"
	ErrOperationInProgress = "ERR_OPERATION_IN_PROGRESS"
	ErrAlreadyRunning      = "ERR_ALREADY_RUNNING"
	ErrAlreadyStopped      = "ERR_ALREADY_STOPPED"

	// Docker Errors
	ErrImageNotFound     = "ERR_IMAGE_NOT_FOUND"
	ErrImagePullFailed   = "ERR_IMAGE_PULL_FAILED"
	ErrContainerFailed   = "ERR_CONTAINER_FAILED"
	ErrDockerUnavailable = "ERR_DOCKER_UNAVAILABLE"
	ErrDockerTimeout     = "ERR_DOCKER_TIMEOUT"

	// Operation Errors
	ErrOperationCancelled = "ERR_OPERATION_CANCELLED"
	ErrOperationTimeout   = "ERR_OPERATION_TIMEOUT"
	ErrRollbackFailed     = "ERR_ROLLBACK_FAILED"
	ErrPartialFailure     = "ERR_PARTIAL_FAILURE"

	// Discovery Errors
	ErrComposeParse  = "ERR_COMPOSE_PARSE"
	ErrEnvResolution = "ERR_ENV_RESOLUTION"
	ErrSymlinkLoop   = "ERR_SYMLINK_LOOP"

	// Internal
	ErrInternal           = "ERR_INTERNAL"
	ErrDatabaseUnavailable = "ERR_DB_UNAVAILABLE"
)

type PortConflictDetails struct {
	Port              int    `json:"port"`
	Protocol          string `json:"protocol"`
	BlockingContainer string `json:"blocking_container,omitempty"`
	BlockingProcess   string `json:"blocking_process,omitempty"`
}

type PartialFailureDetails struct {
	Succeeded []string `json:"succeeded"`
	Failed    []string `json:"failed"`
}
