package clipvault

import "path/filepath"

const (
	activeDatabaseDirectoryName   = "database"
	isolatedDatabaseDirectoryName = "isolated"
	databaseFileName              = "clipvault.duckdb"
	databaseKeyFileName           = "clipvault.key"
	databaseRecoveryFileName      = "clipvault.recovery"
	databaseStateFileName         = "clipvault.state"
)

var databaseFileNames = [...]string{
	databaseFileName,
	databaseFileName + ".wal",
	databaseKeyFileName,
	databaseRecoveryFileName,
	databaseStateFileName,
}

func activeDatabaseDirectory(dataDir string) string {
	return filepath.Join(dataDir, activeDatabaseDirectoryName)
}

func isolatedDatabasesDirectory(dataDir string) string {
	return filepath.Join(dataDir, isolatedDatabaseDirectoryName)
}

func isolatedDatabaseDirectory(dataDir, id string) string {
	return filepath.Join(isolatedDatabasesDirectory(dataDir), id)
}

func databaseGroupFile(directory, name string) string {
	return filepath.Join(directory, name)
}

func activeDatabaseFile(dataDir, name string) string {
	return databaseGroupFile(activeDatabaseDirectory(dataDir), name)
}
