package host

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"wox/plugin"
	"wox/util"

	"github.com/google/uuid"
)

func init() {
	host := &ProcessHost{
		pluginMap: util.NewHashMap[string, *ProcessPlugin](),
	}
	plugin.AllHosts = append(plugin.AllHosts, host)
}

type ProcessHost struct {
	pluginMap *util.HashMap[string, *ProcessPlugin]
}

func (p *ProcessHost) GetRuntime(ctx context.Context) plugin.Runtime {
	return "Process"
}

func (p *ProcessHost) Start(ctx context.Context) error {
	util.GetLogger().Info(ctx, "Process host started")
	return nil
}

func (p *ProcessHost) Stop(ctx context.Context) {
	util.GetLogger().Info(ctx, "Process host started")
}

func (p *ProcessHost) IsStarted(ctx context.Context) bool {
	// Script host is always "started" since it doesn't maintain persistent connections
	return true
}

func (p *ProcessHost) LoadPlugin(ctx context.Context, metadata plugin.Metadata, pluginDirectory string) (plugin.Plugin, error) {
	util.GetLogger().Info(ctx, fmt.Sprintf("loading process plugin: %s, pluginDirectory: %s", metadata.Name, pluginDirectory))
	process := exec.Command("cmd", "/B", "/C", metadata.Entry)
	process.Dir = pluginDirectory
	plugin := &ProcessPlugin{
		metadata,
		process,
		nil,
		nil,
	}
	p.pluginMap.Store(metadata.Id, plugin)
	return plugin, nil
}

func (p *ProcessHost) UnloadPlugin(ctx context.Context, metadata plugin.Metadata) {
	util.GetLogger().Info(ctx, fmt.Sprintf("Unloaded process plugin: %s", metadata.Name))
	plugin, exist := p.pluginMap.Load(metadata.Id)
	if !exist {
		util.GetLogger().Error(ctx, fmt.Sprintf("Unloaded process plugin not exist: %s", metadata.Name))
		return
	}
	if plugin.process.Process != nil {
		plugin.rpcClient.Dispose()
		error := plugin.process.Process.Kill()
		if error != nil {
			util.GetLogger().Warn(ctx, fmt.Sprintf("Kill process plugin failed: %s, %s", metadata.Name, error.Error()))
		}
	}
	p.pluginMap.Delete(metadata.Id)
}

type ProcessPlugin struct {
	metatdata  plugin.Metadata
	process    *exec.Cmd
	initParams *plugin.InitParams
	rpcClient  *JsonRpcClient
}

func (pp *ProcessPlugin) Init(ctx context.Context, initParams plugin.InitParams) {
	util.GetLogger().Debug(ctx, fmt.Sprintf("Process plugin %s Init", pp.metatdata.Name))
	pp.initParams = &initParams
	output, error := pp.process.StdoutPipe()
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("Process plugin <%s> Init error, cannot get stdout pipe: %s", pp.metatdata.Name, error.Error()))
		return
	}
	intput, error := pp.process.StdinPipe()
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("Process plugin <%s> Init error, cannot get stdout pipe: %s", pp.metatdata.Name, error.Error()))
		return
	}
	errorPipe, error := pp.process.StderrPipe()
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("Process plugin <%s> Init error, cannot get stderr pipe: %s", pp.metatdata.Name, error.Error()))
		return
	}
	go func() {
		s := bufio.NewScanner(errorPipe)
		for s.Scan() {
			line := s.Text()
			util.GetLogger().Error(ctx, fmt.Sprintf("Process plugin <%s>  stderror: %s", pp.metatdata.Name, line))
		}
	}()

	pp.rpcClient = NewJsonRpcClient(ctx, &output, intput, func(r JsonRpcRequest) *JsonRpcResponse {
		res := pp.onApiCall(r)
		return res
	})
	error = pp.process.Start()
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("Process plugin <%s> Init error, start failed: %s", pp.metatdata.Name, error.Error()))
	}
}

func (pp *ProcessPlugin) Query(ctx context.Context, query plugin.Query) []plugin.QueryResult {
	selectionJson, marshalErr := json.Marshal(query.Selection)
	if marshalErr != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("[%s] failed to marshal plugin query selection: %s", pp.metatdata.Name, marshalErr.Error()))
		return []plugin.QueryResult{}
	}

	envJson, marshalEnvErr := json.Marshal(query.Env)
	if marshalEnvErr != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("[%s] failed to marshal plugin query env: %s", pp.metatdata.Name, marshalEnvErr.Error()))
		return []plugin.QueryResult{}
	}

	response, queryError := pp.rpcClient.SendRequest(ctx, JsonRpcRequest{
		Id:     uuid.NewString(),
		Method: "Query",
		Type:   JsonRpcTypeRequest,
		Params: map[string]string{
			"Type":           query.Type,
			"RawQuery":       query.RawQuery,
			"TriggerKeyword": query.TriggerKeyword,
			"Command":        query.Command,
			"Search":         query.Search,
			"Selection":      string(selectionJson),
			"Env":            string(envJson),
		},
	})
	util.GetLogger().Debug(ctx, fmt.Sprintf("[%s] message send, %s", pp.metatdata.Name, query.RawQuery))

	if queryError != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("[%s] query failed: %s", pp.metatdata.Name, queryError.Error()))
		return []plugin.QueryResult{
			plugin.GetPluginManager().GetResultForFailedQuery(ctx, pp.metatdata, query, queryError),
		}
	}

	var results []plugin.QueryResult
	marshalData, marshalErr := json.Marshal(response.Result)
	if marshalErr != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("[%s] failed to marshal plugin query results: %s", pp.metatdata.Name, marshalErr.Error()))
		return nil
	}
	unmarshalErr := json.Unmarshal(marshalData, &results)
	if unmarshalErr != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("[%s] failed to unmarshal query results: %s", pp.metatdata.Name, unmarshalErr.Error()))
		return []plugin.QueryResult{}
	}
	// TODO support action and refresh
	return results
}

func (pp *ProcessPlugin) onApiCall(request JsonRpcRequest) *JsonRpcResponse {
	// TODO
	return nil
}

type JsonRpcClient struct {
	requestMap *util.HashMap[string, chan JsonRpcResponse]
	output     io.WriteCloser
	onRequest  func(JsonRpcRequest) *JsonRpcResponse
	endChan    chan struct{}
}

func NewJsonRpcClient(ctx context.Context, input *io.ReadCloser, output io.WriteCloser, onRequest func(JsonRpcRequest) *JsonRpcResponse) *JsonRpcClient {
	end := make(chan struct{})
	client := &JsonRpcClient{
		util.NewHashMap[string, chan JsonRpcResponse](),
		output,
		onRequest,
		end,
	}
	inputLine := make(chan string)
	go func() {
		s := bufio.NewScanner(*input)
		util.GetLogger().Debug(ctx, fmt.Sprintf("json rpc message start reading"))
		for s.Scan() {
			line := s.Text()
			util.GetLogger().Debug(ctx, fmt.Sprintf("json rpc message recevied: %s", line))
			inputLine <- line
		}
		util.GetLogger().Debug(ctx, fmt.Sprintf("json rpc message recevie loop end"))
	}()
	go func() {
		for {
			select {
			case <-end:
				return
			case line := <-inputLine:
				if strings.Contains(line, string(JsonRpcTypeRequest)) {
					client.handleRequsetMsg(ctx, line)
				} else {
					client.handleResponseMsg(ctx, line)
				}
			}
		}
	}()
	return client
}

func (pp *JsonRpcClient) handleRequsetMsg(ctx context.Context, rawMsg string) {
	var msg JsonRpcRequest
	error := json.Unmarshal([]byte(rawMsg), &msg)
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("json rpc parse msg error: msg ->%s error ->%s", rawMsg, error.Error()))
		return
	}
	response := pp.onRequest(msg)

	data, error := json.Marshal(response)
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("json rpc serialize response error: msg ->%s error ->%s", rawMsg, error.Error()))
		return
	}
	pp.output.Write(data)
	pp.output.Write([]byte("\n"))
}

func (pp *JsonRpcClient) handleResponseMsg(ctx context.Context, rawMsg string) {
	var msg JsonRpcResponse
	error := json.Unmarshal([]byte(rawMsg), &msg)
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("json rpc parse msg error: msg ->%s error ->%s", rawMsg, error.Error()))
		return
	}
	request, exist := pp.requestMap.Load(msg.Id)
	if !exist {
		util.GetLogger().Error(ctx, fmt.Sprintf("json rpc handle response, request not found error: msg ->%s", rawMsg))
	}
	request <- msg
}

func (pp *JsonRpcClient) SendRequest(ctx context.Context, request JsonRpcRequest) (*JsonRpcResponse, error) {
	data, error := json.Marshal(request)
	util.GetLogger().Debug(ctx, fmt.Sprintf("json rpc send msg: msg ->%s", data))
	if error != nil {
		util.GetLogger().Error(ctx, fmt.Sprintf("json rpc parse msg error: %s", error.Error()))
		return nil, error
	}
	responseChan := make(chan JsonRpcResponse)
	pp.requestMap.Store(request.Id, responseChan)
	pp.output.Write(data)
	io.WriteString(pp.output, "\r\n\r")

	response := <-responseChan
	return &response, nil
}

func (pp *JsonRpcClient) Dispose() {
	close(pp.endChan)
}
