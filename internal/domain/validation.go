package domain

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

func ValidCode(code string) bool {
	if len(code) < 3 || len(code) > 32 {
		return false
	}
	for _, char := range code {
		if !(char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' ||
			char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}
