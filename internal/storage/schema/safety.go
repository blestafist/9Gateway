// Package schema contains small contracts shared by storage and configuration.
package schema

// RequestBodySchemaSafetyMaxBytes is the largest captured body permitted by
// the request_bodies schema. The migration repeats this value as a SQLite
// literal because migrations cannot reference Go constants.
const RequestBodySchemaSafetyMaxBytes int64 = 1 * 1024 * 1024
