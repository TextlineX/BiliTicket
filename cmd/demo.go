package main

import (
	"fmt"
	"log"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/cobra"
)

var (
	demoStartTime = time.Now()
	demoRequestCount int64
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "性能展示 Demo",
	Long:  `启动 Go + Gin 性能展示页面，展示轻量化和高效特性`,
	RunE:  runDemo,
}

func init() {
	rootCmd.AddCommand(demoCmd)
}

func runDemo(cmd *cobra.Command, args []string) error {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	r.GET("/", handleDemoIndex)
	r.GET("/api/metrics", handleDemoMetrics)
	r.GET("/api/ping", handleDemoPing)
	r.GET("/api/benchmark", handleDemoBenchmark)

	log.Println("🚀 Go + Gin 性能展示服务器")
	log.Println("📍 http://localhost:8080")
	log.Printf("📊 Go 版本: %s | CPU: %d cores", runtime.Version(), runtime.NumCPU())

	return r.Run(":8080")
}

func handleDemoIndex(c *gin.Context) {
	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Go + Gin 性能展示</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <style>
        @keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }
        .animate-pulse-slow { animation: pulse 2s ease-in-out infinite; }
    </style>
</head>
<body class="bg-gray-900 text-white min-h-screen">
    <header class="bg-gray-800/80 backdrop-blur-sm border-b border-gray-700 sticky top-0 z-50">
        <div class="max-w-6xl mx-auto px-6 py-4">
            <div class="flex items-center justify-between">
                <div>
                    <h1 class="text-2xl font-bold">
                        <span class="text-green-400">Go</span> + <span class="text-red-400">Gin</span> 性能展示
                    </h1>
                    <p class="text-gray-400 text-sm mt-1">轻量级 Web 框架 · 高并发处理 · 低内存占用</p>
                </div>
                <span class="px-3 py-1 bg-green-500/20 text-green-400 rounded-full text-sm">
                    <span class="inline-block w-2 h-2 bg-green-400 rounded-full mr-1 animate-pulse-slow"></span>
                    运行中
                </span>
            </div>
        </div>
    </header>

    <main class="max-w-6xl mx-auto px-6 py-8">
        <!-- 实时指标 -->
        <section class="mb-10">
            <h2 class="text-xl font-bold mb-4 text-gray-300">📊 实时性能指标</h2>
            <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4">
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">Goroutines</div>
                    <div id="goroutines" class="text-2xl font-bold text-yellow-400">-</div>
                </div>
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">内存占用</div>
                    <div id="memory" class="text-2xl font-bold text-cyan-400">-</div>
                </div>
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">堆对象</div>
                    <div id="heap" class="text-2xl font-bold text-pink-400">-</div>
                </div>
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">QPS</div>
                    <div id="qps" class="text-2xl font-bold text-green-400">-</div>
                </div>
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">总请求</div>
                    <div id="requests" class="text-2xl font-bold text-orange-400">-</div>
                </div>
                <div class="bg-gray-800/50 rounded-xl p-4 border border-gray-700">
                    <div class="text-gray-400 text-xs mb-1">CPU 核心</div>
                    <div id="cpu" class="text-2xl font-bold text-indigo-400">-</div>
                </div>
            </div>
        </section>

        <!-- 核心优势 -->
        <section class="mb-10">
            <h2 class="text-xl font-bold mb-4 text-gray-300">⚡ Go + Gin 核心优势</h2>
            <div class="grid md:grid-cols-3 gap-6">
                <div class="bg-gradient-to-br from-green-900/40 to-green-800/20 rounded-2xl p-6 border border-green-700/30">
                    <div class="text-4xl mb-3">💾</div>
                    <div class="text-3xl font-bold text-green-400 mb-1">~2MB</div>
                    <div class="text-gray-300">内存占用</div>
                </div>
                <div class="bg-gradient-to-br from-blue-900/40 to-blue-800/20 rounded-2xl p-6 border border-blue-700/30">
                    <div class="text-4xl mb-3">🚀</div>
                    <div class="text-3xl font-bold text-blue-400 mb-1">&lt;1ms</div>
                    <div class="text-gray-300">响应延迟</div>
                </div>
                <div class="bg-gradient-to-br from-purple-900/40 to-purple-800/20 rounded-2xl p-6 border border-purple-700/30">
                    <div class="text-4xl mb-3">📦</div>
                    <div class="text-3xl font-bold text-purple-400 mb-1">~10MB</div>
                    <div class="text-gray-300">二进制大小</div>
                </div>
            </div>
        </section>

        <!-- 测试 -->
        <section class="mb-10">
            <h2 class="text-xl font-bold mb-4 text-gray-300">🧪 性能测试</h2>
            <div class="grid md:grid-cols-2 gap-6">
                <div class="bg-gray-800/50 rounded-2xl p-6 border border-gray-700">
                    <div class="flex items-center justify-between mb-4">
                        <h3 class="font-semibold">🏓 Ping 延迟测试</h3>
                        <button onclick="pingTest()" class="px-4 py-2 bg-green-600 hover:bg-green-500 rounded-lg text-sm font-medium transition">执行</button>
                    </div>
                    <div id="ping-result" class="font-mono text-sm bg-gray-900 rounded-lg p-4 h-32 overflow-auto text-gray-400">点击按钮开始测试...</div>
                </div>
                <div class="bg-gray-800/50 rounded-2xl p-6 border border-gray-700">
                    <div class="flex items-center justify-between mb-4">
                        <h3 class="font-semibold">⚡ 计算性能测试</h3>
                        <button onclick="benchmarkTest()" class="px-4 py-2 bg-blue-600 hover:bg-blue-500 rounded-lg text-sm font-medium transition">执行</button>
                    </div>
                    <div id="benchmark-result" class="font-mono text-sm bg-gray-900 rounded-lg p-4 h-32 overflow-auto text-gray-400">点击按钮开始测试...</div>
                </div>
            </div>
        </section>

        <!-- 内存对比 -->
        <section class="mb-10">
            <h2 class="text-xl font-bold mb-4 text-gray-300">💾 内存占用对比</h2>
            <div class="bg-gray-800/50 rounded-2xl p-6 border border-gray-700 space-y-4">
                <div class="flex items-center gap-4"><span class="w-20 text-sm font-medium">Go</span><div class="flex-1 h-6 bg-gray-700 rounded-full overflow-hidden"><div class="h-full bg-green-500 rounded-full" style="width: 3%"></div></div><span class="w-24 text-right text-green-400 text-sm">~2-5 MB</span></div>
                <div class="flex items-center gap-4"><span class="w-20 text-sm font-medium">Node.js</span><div class="flex-1 h-6 bg-gray-700 rounded-full overflow-hidden"><div class="h-full bg-yellow-500 rounded-full" style="width: 60%"></div></div><span class="w-24 text-right text-yellow-400 text-sm">~50-100 MB</span></div>
                <div class="flex items-center gap-4"><span class="w-20 text-sm font-medium">Python</span><div class="flex-1 h-6 bg-gray-700 rounded-full overflow-hidden"><div class="h-full bg-orange-500 rounded-full" style="width: 40%"></div></div><span class="w-24 text-right text-orange-400 text-sm">~30-50 MB</span></div>
                <div class="flex items-center gap-4"><span class="w-20 text-sm font-medium">Java</span><div class="flex-1 h-6 bg-gray-700 rounded-full overflow-hidden"><div class="h-full bg-red-500 rounded-full" style="width: 90%"></div></div><span class="w-24 text-right text-red-400 text-sm">~100-300 MB</span></div>
            </div>
        </section>
    </main>

    <script>
        async function update() {
            const r = await fetch('/api/metrics');
            const d = await r.json();
            document.getElementById('goroutines').textContent = d.num_goroutine;
            document.getElementById('memory').textContent = d.memory_alloc;
            document.getElementById('heap').textContent = d.heap_objects;
            document.getElementById('qps').textContent = d.qps;
            document.getElementById('requests').textContent = d.requests;
            document.getElementById('cpu').textContent = d.num_cpu;
        }
        async function pingTest() {
            const r = document.getElementById('ping-result');
            r.innerHTML = '<span class="text-yellow-400">测试中...</span>';
            const times = [];
            for (let i = 0; i < 20; i++) {
                const s = performance.now();
                await fetch('/api/ping');
                times.push((performance.now() - s).toFixed(2));
            }
            const avg = (times.reduce((a,b) => +a + +b, 0) / times.length).toFixed(2);
            const min = Math.min(...times.map(Number)).toFixed(2);
            const max = Math.max(...times.map(Number)).toFixed(2);
            r.innerHTML = '<span class="text-green-400">✓ 20次请求完成</span>\n\n平均: ' + avg + 'ms | 最小: ' + min + 'ms | 最大: ' + max + 'ms';
        }
        async function benchmarkTest() {
            const r = document.getElementById('benchmark-result');
            r.innerHTML = '<span class="text-yellow-400">计算中...</span>';
            const s = performance.now();
            const res = await fetch('/api/benchmark');
            const d = await res.json();
            const e = performance.now();
            r.innerHTML = '<span class="text-green-400">✓ 计算完成</span>\n\n服务端: ' + d.time_ms.toFixed(2) + 'ms\n客户端: ' + (e - s).toFixed(2) + 'ms\n内存: ' + d.memory_alloc;
        }
        update();
        setInterval(update, 1000);
    </script>
</body>
</html>`
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(200, html)
}

func handleDemoMetrics(c *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	c.JSON(200, gin.H{
		"num_goroutine": runtime.NumGoroutine(),
		"memory_alloc":   formatDemoBytes(m.Alloc),
		"heap_objects":   int(m.HeapObjects),
		"num_cpu":       runtime.NumCPU(),
		"requests":      demoRequestCount,
		"qps":           currentDemoQPS(),
		"uptime":        time.Since(demoStartTime).Round(time.Second).String(),
	})
}

var (
	demoLastCount int64
	demoLastTime  = time.Now()
)

func currentDemoQPS() float64 {
	elapsed := time.Since(demoLastTime).Seconds()
	if elapsed >= 1.0 {
		demoLastCount = demoRequestCount
		demoLastTime = time.Now()
	}
	return float64(demoRequestCount - demoLastCount)
}

func handleDemoPing(c *gin.Context) {
	demoRequestCount++
	c.JSON(200, gin.H{"message": "pong", "server": "Go + Gin"})
}

func handleDemoBenchmark(c *gin.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	start := time.Now()
	var sum int64
	for i := 0; i < 1000000; i++ {
		sum += int64(i)
	}
	elapsed := time.Since(start)
	c.JSON(200, gin.H{
		"iterations":   1000000,
		"time_ms":      elapsed.Seconds() * 1000,
		"result":       sum,
		"goroutines":   runtime.NumGoroutine(),
		"memory_alloc": formatDemoBytes(m.Alloc),
	})
}

func formatDemoBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
