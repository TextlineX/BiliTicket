package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

var (
	port       int
	serverName string
	masterURL  string
	selfIP     string
	share      bool
)

var workerCmd = &cobra.Command{
	Use:   "worker",
	Short: "启动 Worker 模式",
	Long:  `启动 Worker 模式，作为分布式抢票的工作节点`,
	RunE:  runWorker,
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "启动 Server 模式",
	Long:  `启动 Server 模式，支持远程控制和 Web UI`,
	RunE:  runServer,
}

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "启动 GUI 界面",
	Long:  `启动 Gradio 风格的 Web 界面`,
	RunE:  runUI,
}

func init() {
	workerCmd.Flags().IntVar(&port, "port", 8080, "监听端口")
	workerCmd.Flags().StringVar(&masterURL, "master", "", "Master 节点地址")
	workerCmd.Flags().StringVar(&selfIP, "self-ip", "127.0.0.1", "本机IP地址")

	serverCmd.Flags().IntVar(&port, "port", 8080, "监听端口")
	serverCmd.Flags().StringVar(&serverName, "server-name", "", "服务器名称")
	serverCmd.Flags().BoolVar(&share, "share", false, "创建公开链接")
}

func runWorker(cmd *cobra.Command, args []string) error {
	log.Printf("[INFO] 启动 Worker 模式...")
	log.Printf("[INFO] Master: %s", masterURL)
	log.Printf("[INFO] Self IP: %s:%d", selfIP, port)

	// Worker 模式 - 连接 Master 获取任务
	// 实际实现需要 WebSocket 连接
	return startWorkerLoop()
}

func runServer(cmd *cobra.Command, args []string) error {
	log.Printf("[INFO] 启动 Server 模式...")
	log.Printf("[INFO] 端口: %d", port)
	log.Printf("[INFO] 名称: %s", serverName)

	// 注册路由
	http.HandleFunc("/api/status", handleStatus)
	http.HandleFunc("/api/start", handleStart)
	http.HandleFunc("/api/stop", handleStop)
	http.HandleFunc("/api/config", handleConfig)

	// 启动服务器
	addr := fmt.Sprintf(":%d", port)
	log.Printf("[INFO] 服务器启动: http://0.0.0.0%s", addr)
	return http.ListenAndServe(addr, nil)
}

func runUI(cmd *cobra.Command, args []string) error {
	log.Printf("[INFO] 启动 Web UI...")

	// 简单的 Web UI
	html := `<!DOCTYPE html>
<html>
<head>
    <title>B站抢票工具</title>
    <meta charset="utf-8">
    <style>
        body { font-family: Arial, sans-serif; max-width: 800px; margin: 50px auto; padding: 20px; }
        .card { border: 1px solid #ddd; padding: 20px; margin: 10px 0; border-radius: 8px; }
        .status { color: #00a1d6; font-weight: bold; }
        button { background: #00a1d6; color: white; border: none; padding: 10px 20px; cursor: pointer; border-radius: 4px; }
        input, select { padding: 8px; margin: 5px 0; width: 100%; box-sizing: border-box; }
    </style>
</head>
<body>
    <h1>🎫 B站抢票工具</h1>
    <div class="card">
        <h3>抢票配置</h3>
        <label>场馆ID:</label> <input type="text" id="areaId">
        <label>场次ID:</label> <input type="text" id="scheduleId">
        <label>票档ID:</label> <input type="text" id="itemId">
        <label>数量:</label> <input type="number" id="quantity" value="1">
        <label>间隔(ms):</label> <input type="number" id="interval" value="500">
    </div>
    <div class="card">
        <h3>状态: <span class="status" id="status">空闲</span></h3>
        <p id="log">等待开始...</p>
    </div>
    <button onclick="startBuy()">开始抢票</button>
    <button onclick="stopBuy()" style="background: #ff4757;">停止</button>
    <script>
        function startBuy() {
            document.getElementById('status').textContent = '抢票中...';
            document.getElementById('log').textContent = '开始抢票任务...';
        }
        function stopBuy() {
            document.getElementById('status').textContent = '已停止';
        }
    </script>
</body>
</html>`

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(html))
	})

	addr := fmt.Sprintf(":%d", port)
	log.Printf("[INFO] UI 启动: http://localhost%s", addr)
	return http.ListenAndServe(addr, nil)
}

func startWorkerLoop() error {
	// 模拟 Worker 循环
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Printf("[DEBUG] Worker 等待任务...")
		}
	}
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `{"status": "idle", "uptime": 0}`)
}

func handleStart(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `{"code": 0, "message": "任务已启动"}`)
}

func handleStop(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `{"code": 0, "message": "任务已停止"}`)
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `{"code": 0}`)
}
