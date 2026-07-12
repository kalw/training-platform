package dockershim

import "time"

// Minimal Docker Engine API request/response shapes — only the fields the
// PoC's handlers read or write. Deliberately not importing docker/docker's
// full client library to keep this prototype's dependency surface small.

type createContainerRequest struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd"`
	Entrypoint []string          `json:"Entrypoint"`
	Tty        bool              `json:"Tty"`
	OpenStdin  bool              `json:"OpenStdin"`
	Env        []string          `json:"Env"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		Privileged bool `json:"Privileged"`
	} `json:"HostConfig"`
	// NetworkingConfig.EndpointsConfig's keys are the network names PWD
	// expects ContainerIPs to answer under later — see PWD's
	// docker.ContainerCreate/ContainerIPs. Values are unused (empty
	// EndpointSettings in PWD's real calls).
	NetworkingConfig struct {
		EndpointsConfig map[string]struct{} `json:"EndpointsConfig"`
	} `json:"NetworkingConfig"`
}

type createContainerResponse struct {
	Id       string   `json:"Id"`
	Warnings []string `json:"Warnings"`
}

type containerState struct {
	Status     string `json:"Status"`
	Running    bool   `json:"Running"`
	Paused     bool   `json:"Paused"`
	Restarting bool   `json:"Restarting"`
	Pid        int    `json:"Pid"`
	ExitCode   int    `json:"ExitCode"`
	StartedAt  string `json:"StartedAt"`
	FinishedAt string `json:"FinishedAt"`
}

type networkEndpoint struct {
	IPAddress string `json:"IPAddress"`
}

type networkSettings struct {
	IPAddress string                     `json:"IPAddress"`
	Networks  map[string]networkEndpoint `json:"Networks"`
}

type containerJSON struct {
	Id      string         `json:"Id"`
	Created string         `json:"Created"`
	Name    string         `json:"Name"`
	Image   string         `json:"Image"`
	State   containerState `json:"State"`
	Config  struct {
		Image string `json:"Image"`
		Tty   bool   `json:"Tty"`
	} `json:"Config"`
	NetworkSettings networkSettings `json:"NetworkSettings"`
}

type execCreateRequest struct {
	Cmd          []string `json:"Cmd"`
	Tty          bool     `json:"Tty"`
	AttachStdin  bool     `json:"AttachStdin"`
	AttachStdout bool     `json:"AttachStdout"`
	AttachStderr bool     `json:"AttachStderr"`
}

type execCreateResponse struct {
	Id string `json:"Id"`
}

type execStartRequest struct {
	Detach bool `json:"Detach"`
	Tty    bool `json:"Tty"`
}

type execInspectResponse struct {
	ID       string `json:"ID"`
	Running  bool   `json:"Running"`
	ExitCode int    `json:"ExitCode"`
	Pid      int    `json:"Pid"`
}

type versionResponse struct {
	Version       string `json:"Version"`
	ApiVersion    string `json:"ApiVersion"`
	MinAPIVersion string `json:"MinAPIVersion"`
	GitCommit     string `json:"GitCommit"`
	Os            string `json:"Os"`
	Arch          string `json:"Arch"`
	BuildTime     string `json:"BuildTime"`
}

type networkCreateRequest struct {
	Name string `json:"Name"`
}

type networkCreateResponse struct {
	Id string `json:"Id"`
}

type networkConnectRequest struct {
	Container string `json:"Container"`
}

func nowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}
