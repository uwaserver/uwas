package database

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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
	out, err := exec.Command("docker", "info", "--format", "{{.ServerVersion}}").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// DockerVersion returns Docker version string.
func DockerVersion() string {
	out, _ := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Output()
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
	if out, _ := exec.Command("docker", "ps", "-a", "--filter", "name="+containerName, "--format", "{{.ID}}").Output(); len(strings.TrimSpace(string(out))) > 0 {
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

	out, err := exec.Command("docker", args...).CombinedOutput()
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
	out, err := exec.Command("docker", "ps", "-a",
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
	out, err := exec.Command("docker", "start", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker start: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// StopDockerDB stops a running container.
func StopDockerDB(name string) error {
	out, err := exec.Command("docker", "stop", "-t", "10", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// RemoveDockerDB stops and removes a container.
func RemoveDockerDB(name string) error {
	// Stop first (ignore error if already stopped)
	exec.Command("docker", "stop", "-t", "5", containerPrefix+name).Run()
	time.Sleep(500 * time.Millisecond)

	out, err := exec.Command("docker", "rm", "-f", containerPrefix+name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %s — %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}
