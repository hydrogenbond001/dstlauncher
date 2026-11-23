package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type clusterSpec struct {
	DisplayName string
	Root        string
	ClusterName string
	Token       string
	GameMode    string
	MaxPlayers  int
	PvP         bool
	Password    string
	MasterPort  int
	CavesPort   int
	ShardPort   int
	EnableCaves bool
}

func createCluster(spec clusterSpec) (string, error) {
	root := strings.TrimSpace(spec.Root)
	name := strings.TrimSpace(spec.ClusterName)
	if root == "" {
		return "", fmt.Errorf("集群根目录不能为空")
	}
	if name == "" {
		return "", fmt.Errorf("集群文件夹名不能为空")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("集群文件夹名不能包含路径分隔符")
	}
	if spec.MaxPlayers == 0 {
		spec.MaxPlayers = 6
	}
	if spec.MaxPlayers < 1 || spec.MaxPlayers > 64 {
		return "", fmt.Errorf("最大玩家数需在 1-64 之间")
	}
	if spec.GameMode == "" {
		spec.GameMode = "survival"
	}
	switch spec.GameMode {
	case "survival", "endless", "wilderness":
	default:
		return "", fmt.Errorf("游戏模式必须是 survival / endless / wilderness")
	}
	if spec.MasterPort == 0 {
		spec.MasterPort = 10999
	}
	if spec.CavesPort == 0 {
		spec.CavesPort = 10998
	}
	if spec.ShardPort == 0 {
		spec.ShardPort = 10888
	}
	ports := []int{spec.MasterPort, spec.ShardPort}
	if spec.EnableCaves {
		ports = append(ports, spec.CavesPort)
	}
	seen := map[int]bool{}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("端口无效: %d", port)
		}
		if seen[port] {
			return "", fmt.Errorf("端口重复: %d", port)
		}
		seen[port] = true
	}

	clusterDir := filepath.Join(root, name)
	if _, err := os.Stat(clusterDir); err == nil {
		return "", fmt.Errorf("目录已存在: %s", clusterDir)
	}
	if err := os.MkdirAll(clusterDir, 0o755); err != nil {
		return "", err
	}
	cleanup := func() { _ = os.RemoveAll(clusterDir) }

	write := func(rel, content string) error {
		path := filepath.Join(clusterDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(content), 0o644)
	}

	clusterINI := fmt.Sprintf(`[GAMEPLAY]
game_mode = %s
max_players = %d
pvp = %t
pause_when_empty = true

[NETWORK]
cluster_name = %s
cluster_description =
cluster_password = %s
cluster_intention = cooperative
offline_cluster = false
cluster_language = zh

[MISC]
console_enabled = true

[SHARD]
shard_enabled = %t
bind_ip = 127.0.0.1
master_ip = 127.0.0.1
master_port = %d
cluster_key = defaultPass
`, spec.GameMode, spec.MaxPlayers, spec.PvP, iniValue(spec.DisplayName), iniValue(spec.Password), spec.EnableCaves, spec.ShardPort)
	if err := write("cluster.ini", clusterINI); err != nil {
		cleanup()
		return "", err
	}
	masterINI := fmt.Sprintf(`[NETWORK]
server_port = %d

[SHARD]
is_master = true

[ACCOUNT]
encode_user_path = true
`, spec.MasterPort)
	if err := write(filepath.Join("Master", "server.ini"), masterINI); err != nil {
		cleanup()
		return "", err
	}
	if err := write(filepath.Join("Master", "worldgenoverride.lua"), `return {
  override_enabled = true,
  preset = "SURVIVAL_TOGETHER",
}
`); err != nil {
		cleanup()
		return "", err
	}
	if spec.EnableCaves {
		cavesINI := fmt.Sprintf(`[NETWORK]
server_port = %d

[SHARD]
is_master = false
name = Caves

[ACCOUNT]
encode_user_path = true
`, spec.CavesPort)
		if err := write(filepath.Join("Caves", "server.ini"), cavesINI); err != nil {
			cleanup()
			return "", err
		}
		if err := write(filepath.Join("Caves", "worldgenoverride.lua"), `return {
  override_enabled = true,
  preset = "DST_CAVE",
}
`); err != nil {
			cleanup()
			return "", err
		}
	}
	token := strings.TrimRight(strings.TrimSpace(spec.Token), "\r\n")
	if token != "" {
		token += "\n"
	}
	if err := write("cluster_token.txt", token); err != nil {
		cleanup()
		return "", err
	}
	if err := write("adminlist.txt", ""); err != nil {
		cleanup()
		return "", err
	}
	return clusterDir, nil
}

func iniValue(v string) string {
	v = strings.ReplaceAll(v, "\r", " ")
	return strings.ReplaceAll(v, "\n", " ")
}
