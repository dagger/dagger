
package session

// GitCredentialError represents an error from the git credential service
type GitCredentialError struct {
	Type    ErrorInfo_ErrorType
	Message string
}

func (e *GitCredentialError) Error() string {
	return e.Message
}

// IsCredentialNotFound checks if the error is a credential not found error
func IsCredentialNotFound(err error) bool {
	credErr, ok := err.(*GitCredentialError)
	return ok && credErr.Type == CREDENTIAL_NOT_FOUND
}
