package contextkeys

// Key is a type for context keys to avoid collisions
type Key string

// LanguageKey is the context key for the language value
const LanguageKey Key = "language"

// ViewerKey holds the *models.Viewer of the signed-in user.
const ViewerKey Key = "viewer"
