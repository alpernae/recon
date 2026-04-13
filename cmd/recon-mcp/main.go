package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/alpernae/recon/internal/toolchain"
)

type MCPRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	Id      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

type MCPResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	Id      int    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   any    `json:"error,omitempty"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req MCPRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			continue
		}

		if req.Method == "initialize" {
			reply(req.Id, map[string]any{
				"capabilities": map[string]any{},
				"serverInfo":   map[string]any{"name": "recon-mcp", "version": "1.0"},
			})
		} else if req.Method == "tools/list" {
			toolsList := []map[string]any{{
				"name":        "run_recon",
				"description": "Run recon against a domain",
				"inputSchema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"domain": map[string]any{
							"type":        "string",
							"description": "The target domain to scan",
						},
					},
					"required": []string{"domain"},
				},
			}}

			for _, t := range toolchain.KnownTools() {
				toolsList = append(toolsList, map[string]any{
					"name":        "run_" + t.Name,
					"description": "Run the " + t.Name + " tool",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"args": map[string]any{
								"type": "array",
								"items": map[string]any{
									"type": "string",
								},
								"description": "Arguments to pass to the tool",
							},
						},
					},
				})
			}
			reply(req.Id, map[string]any{
				"tools": toolsList,
			})
		} else if req.Method == "tools/call" {
			params, ok := req.Params.(map[string]any)
			if !ok {
				replyError(req.Id, "invalid params")
				continue
			}
			name, _ := params["name"].(string)
			if name == "run_recon" {
				args, ok := params["arguments"].(map[string]any)
				if !ok {
					replyError(req.Id, "invalid arguments")
					continue
				}
				domain, _ := args["domain"].(string)
				if domain == "" {
					replyError(req.Id, "missing domain")
					continue
				}

				// Execute recon.exe externally, pipe logs to Stderr to preserve Stdout for MCP
				cmd := exec.Command("recon.exe", domain, "--fast")
				cmd.Stdout = os.Stderr
				cmd.Stderr = os.Stderr
				err := cmd.Run()

				output := fmt.Sprintf("Recon execution finished for %s (error: %v).\n\n", domain, err)

				livePath := filepath.Join(fmt.Sprintf("output-%s", domain), "live", "live.txt")
				if b, err := os.ReadFile(livePath); err == nil {
					output += "Live hosts:\n" + string(b)
				} else {
					output += "No live hosts found.\n"
				}

				reply(req.Id, map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": output,
					}},
				})
			} else if strings.HasPrefix(name, "run_") {
				toolName := strings.TrimPrefix(name, "run_")
				argsMap, _ := params["arguments"].(map[string]any)

				var args []string
				if argsMap != nil {
					if argsRaw, ok := argsMap["args"].([]any); ok {
						for _, a := range argsRaw {
							args = append(args, fmt.Sprint(a))
						}
					}
				}

				cmd := exec.Command(toolName, args...)
				out, err := cmd.CombinedOutput()

				output := string(out)
				if err != nil {
					output += fmt.Sprintf("\nError: %v\n", err)
				}
				if output == "" {
					output = fmt.Sprintf("%s executed successfully but produced no stdout.", toolName)
				}

				reply(req.Id, map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": output,
					}},
				})
			} else {
				replyError(req.Id, "tool not found")
			}
		} else {
			replyError(req.Id, "method not found")
		}
	}
}

func reply(id int, result any) {
	resp := MCPResponse{Jsonrpc: "2.0", Id: id, Result: result}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}

func replyError(id int, msg string) {
	resp := MCPResponse{Jsonrpc: "2.0", Id: id, Error: map[string]any{"code": -32603, "message": msg}}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}
