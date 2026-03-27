package database

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Testable hook for docker.go — replaced in tests.
var dockerExecCommandFn = exec.Command

// DockerDBEngine represents a supported database engine.
type DockerDBEngine string

const (
	EngineMariaDB    DockerDBEngine = "mariadb"
	EngineMySQL      DockerDBEngine = "mysql"
	EnginePostgreSQL DockerDBEngine = "postgresql"
)

// DockerDBContainer represents a running database container.
type DockerDBContainer struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Engine   DockerDBEngine `json:"engine"`
	Image    string         `json:"image"`
	Port     int            `json:"port"`
	Status   string         `json:"status"`
	Running  bool           `json:"running"`
	RootPass string         `json:"root_pass,omitempty"`
	DataDir  string         `json:"data_dir,omitempty"`
}

const containerPrefix = "uwas-db-"

// DockerAvailable checks if Docker is installed and running.
func DockerAvailable() bool {
	out, err := dockerExecCommandFn("docker", "info", "--format", "{{.ServerVersion}}").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// DockerVersion returns Docker version string.
func DockerVersion() string {
	out, _ := dockerExecCommandFn("docker", "version", "--format", "{{.Server.Version}}").Output()
	return strings.TrimSpace(string(out))
}

// imageForEngine returns the Docker image for a given engine.
func imageForEngine(engine DockerDBEngine) string {
	switch engine {
	case EngineMariaDB:
		return "mariadb:11"
	case EngineMySQL:
		return "mysql:8"
	case EnginePostgreSQL:
		return "postgres:16"
	default:
		return ""
	}
}

// CreateDockerDB creates and starts a new database container.
func CreateDockerDB(engine DockerDBEngine, name string, port int, rootPass, dataDir string) (*DockerDBContainer, error) {
	image := imageForEngine(engine)
	if image == "" {
		return nil, fmt.Errorf("unsupported engine: %s", engine)
	}

	containerName := containerPrefix + name

	// Check if already exists
	if out, _ := dockerExecCommandFn("docker", "ps", "-a", "--filter", "name="+containerName, "--format", "{{.ID}}").Output(); len(strings.TrimSpace(string(out))) > 0 {
		return nil, fmt.Errorf("container %s already exists", containerName)
	}

	args := []string{
		"run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", port, defaultPort(engine)),
		"--restart", "unless-stopped",
	}

	if dataDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:/var/lib/%s", dataDir, volumePath(engine)))
	}

	// Engine-specific env vars
	switch engine {
	case EngineMariaDB, EngineMySQL:
		args = append(args, "-e", "MYSQL_ROOT_PASSWORD="+rootPass)
	case EnginePostgreSQL:
		args = append(args, "-e", "POSTGRES_PASSWORD="+rootPass)
	}

	args = append(args, image)

	out, err := dockerExecCommandFn("docker", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("docker run: %s — %w", strings.TrimSpace(string(out)), err)
	}

	containerID := strings.TrimSpace(string(out))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}

	return &DockerDBContainer{
		ID:       containerID,
		Name:     containerName,
		Engine:   engine,
		Image:    image,
		Port:     port,
		Status:   "running",
		Running:  true,
		RootPass: rootPass,
		DataDir:  dataDir,
	}, nil
}

func defaultPort(engine DockerDBEngine) int {
	switch engine {
	case EnginePostgreSQL:
		return 5432
	default:
		return 3306
	}
}

func volumePath(engine DockerDBEngine) string {
	switch engine {
	case EnginePostgreSQL:
		return "postgresql/data"
	default:
		return "mysql"
	}
}

// ListDockerDBs lists all UWAS-managed database containers.
func ListDockerDBs() ([]DockerDBContainer, error) {
	out, err := dockerExecCommandFn("docker", "ps", "-a",
		"--filter", "name="+containerPrefix,
		"--format", "{{json .}}",
	).Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	var containers []DockerDBContainer
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var raw struct {
			ID     string `json:"ID"`
			Names  string `json:"Names"`
			Image  string `json:"Image"`
			Status string `json:"Status"`
			Ports  string `json:"Ports"`
			State  string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		c := DockerDBContainer{
			ID:      raw.ID,
			Name:    raw.Names,
			Image:   raw.Image,
			Status:  raw.Status,
			Running: raw.State == "running",
		}

		// Detect engine from image
		switch {
		case strings.Contains(raw.Image, "mariadb"):
			c.Engine = EngineMariaDB
		case strings.Contains(raw.Image, "mysql"):
			c.Engine = EngineMySQL
		case strings.Contains(raw.Image, "postgres"):
			c.Engine = EnginePostgreSQL
		}

		containers = append(containers, c)
	}
	return containers, nil
}

// StartDockerDB starts a stopped container.
func StartDockerDB(name string) error {
	out, err := dockerExecCommandFn("docker", "start", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker start: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// StopDockerDB stops a running container.
func StopDockerDB(name string) error {
	out, err := dockerExecCommandFn("docker", "stop", "-t", "10", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// DockerDBExecSQL runs SQL inside a Docker database container.
func DockerDBExecSQL(containerName, sql string) (string, error) {
	fullName := containerName
	if !strings.HasPrefix(fullName, containerPrefix) {
		fullName = containerPrefix + containerName
	}

	// Detect engine from container image
	inspectOut, err := dockerExecCommandFn("docker", "inspect", "--format", "{{.Config.Image}}", fullName).Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect: %w", err)
	}
	image := strings.TrimSpace(string(inspectOut))

	var args []string
	switch {
	case strings.Contains(image, "postgres"):
		args = []string{"docker", "exec", fullName, "psql", "-U", "postgres", "-t", "-c", sql}
	default: // mariadb, mysql
		args = []string{"docker", "exec", fullName, "mariadb", "-u", "root", "-p$MYSQL_ROOT_PASSWORD",
			"--batch", "--skip-column-names", "-e", sql}
	}

	cmd := dockerExecCommandFn(args[0], args[1:]...)
	// Pass through env from container
	if !strings.Contains(image, "postgres") {
		cmd = dockerExecCommandFn("docker", "exec", fullName, "sh", "-c",
			fmt.Sprintf(`mariadb -u root -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names -e '%s' 2>/dev/null || mysql -u root -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names -e '%s' 2>/dev/null`,
				strings.ReplaceAll(sql, "'", "'\"'\"'"),
				strings.ReplaceAll(sql, "'", "'\"'\"'")))
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("docker exec sql: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// DockerDBListDatabases lists databases in a Docker container.
func DockerDBListDatabases(containerName string) ([]DBInfo, error) {
	sql := `SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN ('information_schema','mysql','performance_schema','sys')`
	out, err := DockerDBExecSQL(containerName, sql)
	if err != nil {
		return nil, err
	}
	var dbs []DBInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		dbs = append(dbs, DBInfo{Name: strings.TrimSpace(line)})
	}
	return dbs, nil
}

// DockerDBCreateDatabase creates a database inside a Docker container.
func DockerDBCreateDatabase(containerName, dbName, user, password string) (*CreateResult, error) {
	if password == "" {
		password = generateDBPassword()
	}
	if user == "" {
		user = dbName
	}
	sql := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci; "+
			"CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; "+
			"GRANT ALL ON %s.* TO '%s'@'%%'; FLUSH PRIVILEGES;",
		backtick(dbName), user, escapeSQL(password), backtick(dbName), user)

	_, err := DockerDBExecSQL(containerName, sql)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Name: dbName, User: user, Password: password, Host: containerName}, nil
}

// DockerDBDropDatabase drops a database from a Docker container.
func DockerDBDropDatabase(containerName, dbName string) error {
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS %s;", backtick(dbName))
	_, err := DockerDBExecSQL(containerName, sql)
	return err
}

// RemoveDockerDB stops and removes a container.
func RemoveDockerDB(name string) error {
	// Stop first (ignore error if already stopped)
	dockerExecCommandFn("docker", "stop", "-t", "5", containerPrefix+name).Run()
	time.Sleep(500 * time.Millisecond)

	out, err := dockerExecCommandFn("docker", "rm", "-f", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
