package metrics

const (
	StageUpload   = "upload"
	StageSelect   = "select"
	StageEdit     = "edit"
	StageProcess  = "process"
	StageStorage  = "storage"
	StageDownload = "download"
	StageEmail    = "email"
)

// Processing statuses
const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusInvalid = "invalid"
	StatusExpired = "expired"
)

// Error types
const (
	ErrorTypeFileNotFound = "file_not_found"
	ErrorTypeInvalidFile  = "invalid_file"
	ErrorTypeTimeout      = "timeout"
	ErrorTypePermission   = "permission"
	ErrorTypeServerError  = "server_error"
)
