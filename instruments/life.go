package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Payload struct {
	Params map[string]string `json:"params"`
	Seed   int64             `json:"seed"`
}

type LifeResponse struct {
	State      string `json:"state"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	Generation int    `json:"generation"`
	Alive      int    `json:"alive"`
	Pattern    string `json:"pattern,omitempty"`
	Wrap       bool   `json:"wrap"`
}

type point struct {
	x int
	y int
}

var lifePatterns = map[string][]point{
	"glider": {
		{1, 0}, {2, 1}, {0, 2}, {1, 2}, {2, 2},
	},
	"blinker": {
		{0, 1}, {1, 1}, {2, 1},
	},
	"toad": {
		{1, 1}, {2, 1}, {3, 1}, {0, 2}, {1, 2}, {2, 2},
	},
	"pulsar": {
		{2, 0}, {3, 0}, {4, 0}, {8, 0}, {9, 0}, {10, 0},
		{0, 2}, {5, 2}, {7, 2}, {12, 2},
		{0, 3}, {5, 3}, {7, 3}, {12, 3},
		{0, 4}, {5, 4}, {7, 4}, {12, 4},
		{2, 5}, {3, 5}, {4, 5}, {8, 5}, {9, 5}, {10, 5},
		{2, 7}, {3, 7}, {4, 7}, {8, 7}, {9, 7}, {10, 7},
		{0, 8}, {5, 8}, {7, 8}, {12, 8},
		{0, 9}, {5, 9}, {7, 9}, {12, 9},
		{0, 10}, {5, 10}, {7, 10}, {12, 10},
		{2, 12}, {3, 12}, {4, 12}, {8, 12}, {9, 12}, {10, 12},
	},
	"acorn": {
		{1, 0}, {3, 1}, {0, 2}, {1, 2}, {4, 2}, {5, 2}, {6, 2},
	},
	"r-pentomino": {
		{1, 0}, {2, 0}, {0, 1}, {1, 1}, {1, 2},
	},
	"lwss": {
		{1, 0}, {2, 0}, {3, 0}, {4, 0},
		{0, 1}, {4, 1},
		{4, 2},
		{0, 3}, {3, 3},
	},
	"gosper": {
		{24, 0},
		{22, 1}, {24, 1},
		{12, 2}, {13, 2}, {20, 2}, {21, 2}, {34, 2}, {35, 2},
		{11, 3}, {15, 3}, {20, 3}, {21, 3}, {34, 3}, {35, 3},
		{0, 4}, {1, 4}, {10, 4}, {16, 4}, {20, 4}, {21, 4},
		{0, 5}, {1, 5}, {10, 5}, {14, 5}, {16, 5}, {17, 5}, {22, 5}, {24, 5},
		{10, 6}, {16, 6}, {24, 6},
		{11, 7}, {15, 7},
		{12, 8}, {13, 8},
	},
}

func main() {
	payload := readPayload()
	op := strings.ToLower(strings.TrimSpace(payload.Params["op"]))

	switch op {
	case "", "ui":
		handleUI()
	case "seed":
		handleSeed(payload)
	case "step":
		handleStep(payload)
	case "patterns":
		handlePatterns()
	default:
		writeJSON(map[string]string{"error": "unknown op"})
	}
}

func readPayload() Payload {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	var b strings.Builder
	for scanner.Scan() {
		b.WriteString(scanner.Text())
	}
	var payload Payload
	_ = json.Unmarshal([]byte(b.String()), &payload)
	if payload.Params == nil {
		payload.Params = map[string]string{}
	}
	return payload
}

func handleSeed(payload Payload) {
	width := clampInt(parseInt(payload.Params["width"], 96), 16, 256)
	height := clampInt(parseInt(payload.Params["height"], 54), 16, 144)
	pattern := strings.ToLower(strings.TrimSpace(payload.Params["pattern"]))
	if pattern == "" {
		pattern = "random"
	}
	density := clampFloat(parseFloat(payload.Params["density"], 0.23), 0.01, 0.95)
	wrap := parseBool(payload.Params["wrap"])
	grid := makeGrid(width, height)
	seedGrid(grid, pattern, density, payload.Seed)
	writeJSON(LifeResponse{
		State:      encodeGrid(grid),
		Width:      width,
		Height:     height,
		Generation: 0,
		Alive:      countAlive(grid),
		Pattern:    pattern,
		Wrap:       wrap,
	})
}

func handleStep(payload Payload) {
	grid := decodeGrid(payload.Params["state"])
	if len(grid) == 0 {
		writeJSON(map[string]string{"error": "missing or invalid state"})
		return
	}
	wrap := parseBool(payload.Params["wrap"])
	generation := parseInt(payload.Params["generation"], 0)
	next := stepGrid(grid, wrap)
	writeJSON(LifeResponse{
		State:      encodeGrid(next),
		Width:      len(next[0]),
		Height:     len(next),
		Generation: generation + 1,
		Alive:      countAlive(next),
		Wrap:       wrap,
	})
}

func handlePatterns() {
	patterns := make([]string, 0, len(lifePatterns)+1)
	patterns = append(patterns, "random")
	for name := range lifePatterns {
		patterns = append(patterns, name)
	}
	writeJSON(patterns)
}

func handleUI() {
	fmt.Print(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>WASIO · Conway's Game of Life</title>
  <style>
    :root {
      --bg: #07110d;
      --panel: rgba(8, 24, 18, 0.88);
      --line: rgba(124, 255, 179, 0.14);
      --glow: #7cffb3;
      --text: #dbffe9;
      --muted: #8eb69b;
      --accent: #ffdf6e;
      --danger: #ff6b6b;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      color: var(--text);
      background:
        radial-gradient(circle at top, rgba(90, 255, 160, 0.15), transparent 35%),
        linear-gradient(180deg, #081612, #040807 48%, #020504);
      min-height: 100vh;
    }
    .shell {
      max-width: 1400px;
      margin: 0 auto;
      padding: 24px;
      display: grid;
      grid-template-columns: 320px 1fr;
      gap: 20px;
    }
    .panel {
      background: var(--panel);
      border: 1px solid rgba(124,255,179,0.14);
      box-shadow: 0 0 0 1px rgba(124,255,179,0.05), 0 18px 60px rgba(0,0,0,0.45);
      border-radius: 18px;
      backdrop-filter: blur(12px);
    }
    .controls { padding: 20px; }
    .title {
      font-size: 28px;
      margin: 0 0 8px;
      letter-spacing: 0.05em;
      text-transform: uppercase;
      color: var(--glow);
      text-shadow: 0 0 18px rgba(124,255,179,0.25);
    }
    .sub { margin: 0 0 18px; color: var(--muted); line-height: 1.5; }
    .group { margin-bottom: 16px; }
    label {
      display: block;
      color: var(--muted);
      margin-bottom: 6px;
      font-size: 12px;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }
    input, select {
      width: 100%;
      background: rgba(255,255,255,0.04);
      border: 1px solid rgba(124,255,179,0.15);
      border-radius: 12px;
      color: var(--text);
      padding: 11px 12px;
      font: inherit;
    }
    .row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; }
    .toggle { display: flex; align-items: center; gap: 10px; color: var(--muted); font-size: 13px; }
    .toggle input { width: auto; accent-color: var(--glow); }
    .actions { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 20px; }
    button {
      border: 0;
      border-radius: 12px;
      padding: 12px 14px;
      font: inherit;
      cursor: pointer;
      transition: transform .12s ease, opacity .12s ease, box-shadow .12s ease;
    }
    button:hover { transform: translateY(-1px); }
    .primary { background: linear-gradient(135deg, #7cffb3, #39c975); color: #062213; box-shadow: 0 10px 30px rgba(57,201,117,0.24); }
    .secondary { background: rgba(255,255,255,0.06); color: var(--text); border: 1px solid rgba(124,255,179,0.12); }
    .danger { background: rgba(255,107,107,0.14); color: #ffd3d3; border: 1px solid rgba(255,107,107,0.25); }
    .stage { padding: 18px; position: relative; overflow: hidden; }
    .hud {
      display: flex;
      flex-wrap: wrap;
      gap: 12px;
      margin-bottom: 16px;
    }
    .stat {
      min-width: 130px;
      padding: 12px 14px;
      border-radius: 14px;
      background: rgba(255,255,255,0.04);
      border: 1px solid rgba(124,255,179,0.1);
    }
    .stat b { display: block; color: var(--glow); font-size: 22px; margin-bottom: 4px; }
    .stat span { color: var(--muted); font-size: 12px; text-transform: uppercase; letter-spacing: .08em; }
    .canvas-wrap {
      border-radius: 20px;
      overflow: hidden;
      background:
        linear-gradient(180deg, rgba(255,255,255,0.02), rgba(255,255,255,0.01)),
        linear-gradient(0deg, var(--line) 1px, transparent 1px),
        linear-gradient(90deg, var(--line) 1px, transparent 1px),
        #020705;
      background-size: auto, 16px 16px, 16px 16px, auto;
      border: 1px solid rgba(124,255,179,0.12);
      box-shadow: inset 0 0 50px rgba(0,0,0,0.35);
      position: relative;
    }
    .canvas-wrap::after {
      content: '';
      position: absolute;
      inset: 0;
      pointer-events: none;
      background: linear-gradient(180deg, rgba(255,255,255,0.02), transparent 18%, transparent 82%, rgba(255,255,255,0.02));
      mix-blend-mode: screen;
    }
    canvas { display: block; width: 100%; image-rendering: pixelated; }
    .footer-note {
      margin-top: 12px;
      color: var(--muted);
      font-size: 12px;
      line-height: 1.6;
    }
    .status-live { color: var(--glow); }
    .status-paused { color: var(--accent); }
    .status-error { color: var(--danger); }
    @media (max-width: 980px) {
      .shell { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <aside class="panel controls">
      <h1 class="title">Conway Life</h1>
      <p class="sub">Server-streamed generations via SSE. The browser only renders. The actual evolution step runs inside WASM.</p>

      <div class="group">
        <label for="pattern">Pattern</label>
        <select id="pattern">
          <option value="random">Random soup</option>
          <option value="glider">Glider</option>
          <option value="blinker">Blinker</option>
          <option value="toad">Toad</option>
          <option value="pulsar">Pulsar</option>
          <option value="acorn">Acorn</option>
          <option value="r-pentomino">R-pentomino</option>
          <option value="lwss">Lightweight spaceship</option>
          <option value="gosper">Gosper glider gun</option>
        </select>
      </div>

      <div class="row group">
        <div>
          <label for="width">Width</label>
          <input id="width" type="number" min="16" max="256" value="96">
        </div>
        <div>
          <label for="height">Height</label>
          <input id="height" type="number" min="16" max="144" value="54">
        </div>
      </div>

      <div class="row group">
        <div>
          <label for="density">Density</label>
          <input id="density" type="number" min="0.01" max="0.95" step="0.01" value="0.23">
        </div>
        <div>
          <label for="interval">Interval ms</label>
          <input id="interval" type="number" min="30" max="2000" step="10" value="90">
        </div>
      </div>

      <div class="row group">
        <div>
          <label for="cell">Cell size</label>
          <input id="cell" type="number" min="4" max="18" step="1" value="10">
        </div>
        <div>
          <label for="limit">Generation cap</label>
          <input id="limit" type="number" min="0" max="50000" step="10" value="0">
        </div>
      </div>

      <div class="group toggle">
        <input id="wrap" type="checkbox" checked>
        <span>Wrap edges toroidally</span>
      </div>

      <div class="actions">
        <button class="primary" id="start-btn">Start</button>
        <button class="secondary" id="pause-btn">Pause</button>
        <button class="secondary" id="resume-btn">Resume</button>
        <button class="danger" id="restart-btn">Restart</button>
      </div>

      <p class="footer-note">
        Presets like <strong>Gosper</strong>, <strong>Pulsar</strong>, and <strong>Acorn</strong> work best on wider boards.
        Resume continues from the exact last streamed generation by sending the current state back to the server.
      </p>
    </aside>

    <main class="panel stage">
      <div class="hud">
        <div class="stat"><b id="gen">0</b><span>Generation</span></div>
        <div class="stat"><b id="alive">0</b><span>Alive cells</span></div>
        <div class="stat"><b id="fps">0</b><span>Frames/sec</span></div>
        <div class="stat"><b id="status" class="status-live">idle</b><span>Status</span></div>
      </div>
      <div class="canvas-wrap">
        <canvas id="life-canvas" width="960" height="540"></canvas>
      </div>
      <p class="footer-note">Tip: start with <strong>Gosper</strong> or <strong>Acorn</strong> for long-lived growth patterns.</p>
    </main>
  </div>

  <script>
    var source = null;
    var currentState = '';
    var currentGeneration = 0;
    var currentWidth = 96;
    var currentHeight = 54;
    var lastFrameAt = 0;
    var canvas = document.getElementById('life-canvas');
    var ctx = canvas.getContext('2d');

    function $(id) { return document.getElementById(id); }
    function status(text, cls) {
      var el = $('status');
      el.textContent = text;
      el.className = cls || '';
    }

    function draw(frame) {
      currentState = frame.state;
      currentGeneration = frame.generation;
      currentWidth = frame.width;
      currentHeight = frame.height;

      var cell = Math.max(4, Math.min(18, parseInt($('cell').value || '10', 10)));
      canvas.width = currentWidth * cell;
      canvas.height = currentHeight * cell;

      ctx.fillStyle = '#03100b';
      ctx.fillRect(0, 0, canvas.width, canvas.height);
      ctx.fillStyle = '#7cffb3';
      ctx.shadowColor = 'rgba(124,255,179,0.35)';
      ctx.shadowBlur = Math.max(1, Math.floor(cell / 2));

      var rows = frame.state.split('/');
      for (var y = 0; y < rows.length; y++) {
        var row = rows[y];
        for (var x = 0; x < row.length; x++) {
          if (row.charCodeAt(x) === 49) {
            ctx.fillRect(x * cell, y * cell, cell - 1, cell - 1);
          }
        }
      }
      ctx.shadowBlur = 0;

      $('gen').textContent = String(frame.generation);
      $('alive').textContent = String(frame.alive);
      var now = performance.now();
      if (lastFrameAt > 0) {
        var fps = 1000 / Math.max(1, now - lastFrameAt);
        $('fps').textContent = fps.toFixed(1);
      }
      lastFrameAt = now;
    }

    function buildParams(includeState) {
      var params = new URLSearchParams();
      params.set('pattern', $('pattern').value);
      params.set('width', $('width').value);
      params.set('height', $('height').value);
      params.set('density', $('density').value);
      params.set('interval', $('interval').value);
      params.set('generations', $('limit').value);
      if ($('wrap').checked) {
        params.set('wrap', '1');
      }
      if (includeState && currentState) {
        params.set('state', currentState);
        params.set('generation', String(currentGeneration));
      }
      return params;
    }

    function openStream(includeState) {
      if (source) {
        source.close();
      }
      var params = buildParams(includeState);
      source = new EventSource('/_life/stream?' + params.toString());
      status('live', 'status-live');
      source.onmessage = function(ev) {
        var frame = JSON.parse(ev.data);
        draw(frame);
      };
      source.addEventListener('generation', function(ev) {
        var frame = JSON.parse(ev.data);
        draw(frame);
      });
      source.addEventListener('done', function() {
        status('done', 'status-paused');
        source.close();
        source = null;
      });
      source.addEventListener('error', function(ev) {
        status('stream error', 'status-error');
      });
      source.onerror = function() {
        if (source && source.readyState === EventSource.CLOSED) {
          status('closed', 'status-paused');
        }
      };
    }

    $('start-btn').addEventListener('click', function() {
      currentState = '';
      currentGeneration = 0;
      lastFrameAt = 0;
      openStream(false);
    });

    $('pause-btn').addEventListener('click', function() {
      if (source) {
        source.close();
        source = null;
      }
      status('paused', 'status-paused');
    });

    $('resume-btn').addEventListener('click', function() {
      if (!currentState) {
        openStream(false);
        return;
      }
      lastFrameAt = 0;
      openStream(true);
    });

    $('restart-btn').addEventListener('click', function() {
      currentState = '';
      currentGeneration = 0;
      lastFrameAt = 0;
      openStream(false);
    });

    openStream(false);
  </script>
</body>
</html>`)
}

func makeGrid(width, height int) [][]bool {
	grid := make([][]bool, height)
	for y := range grid {
		grid[y] = make([]bool, width)
	}
	return grid
}

func seedGrid(grid [][]bool, pattern string, density float64, seed int64) {
	if len(grid) == 0 || len(grid[0]) == 0 {
		return
	}
	if pattern == "random" {
		rng := uint64(seed)
		if rng == 0 {
			rng = 0x9e3779b97f4a7c15
		}
		threshold := uint64(density * 10000)
		for y := range grid {
			for x := range grid[y] {
				rng ^= rng << 13
				rng ^= rng >> 7
				rng ^= rng << 17
				if rng%10000 < threshold {
					grid[y][x] = true
				}
			}
		}
		return
	}
	pts, ok := lifePatterns[pattern]
	if !ok {
		seedGrid(grid, "random", density, seed)
		return
	}
	maxX, maxY := 0, 0
	for _, pt := range pts {
		if pt.x > maxX {
			maxX = pt.x
		}
		if pt.y > maxY {
			maxY = pt.y
		}
	}
	offX := (len(grid[0]) - (maxX + 1)) / 2
	offY := (len(grid) - (maxY + 1)) / 2
	if offX < 0 {
		offX = 0
	}
	if offY < 0 {
		offY = 0
	}
	for _, pt := range pts {
		x := pt.x + offX
		y := pt.y + offY
		if y >= 0 && y < len(grid) && x >= 0 && x < len(grid[y]) {
			grid[y][x] = true
		}
	}
}

func stepGrid(grid [][]bool, wrap bool) [][]bool {
	height := len(grid)
	width := len(grid[0])
	next := makeGrid(width, height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			neighbors := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 {
						continue
					}
					nx := x + dx
					ny := y + dy
					if wrap {
						if nx < 0 {
							nx = width - 1
						} else if nx >= width {
							nx = 0
						}
						if ny < 0 {
							ny = height - 1
						} else if ny >= height {
							ny = 0
						}
						if grid[ny][nx] {
							neighbors++
						}
						continue
					}
					if nx >= 0 && nx < width && ny >= 0 && ny < height && grid[ny][nx] {
						neighbors++
					}
				}
			}
			next[y][x] = neighbors == 3 || (grid[y][x] && neighbors == 2)
		}
	}
	return next
}

func encodeGrid(grid [][]bool) string {
	rows := make([]string, len(grid))
	for y := range grid {
		row := make([]byte, len(grid[y]))
		for x := range grid[y] {
			if grid[y][x] {
				row[x] = '1'
			} else {
				row[x] = '0'
			}
		}
		rows[y] = string(row)
	}
	return strings.Join(rows, "/")
}

func decodeGrid(state string) [][]bool {
	state = strings.TrimSpace(state)
	if state == "" {
		return nil
	}
	rows := strings.Split(state, "/")
	if len(rows) == 0 {
		return nil
	}
	width := len(rows[0])
	if width == 0 {
		return nil
	}
	grid := makeGrid(width, len(rows))
	for y, row := range rows {
		if len(row) != width {
			return nil
		}
		for x := 0; x < len(row); x++ {
			grid[y][x] = row[x] == '1'
		}
	}
	return grid
}

func countAlive(grid [][]bool) int {
	total := 0
	for y := range grid {
		for x := range grid[y] {
			if grid[y][x] {
				total++
			}
		}
	}
	return total
}

func parseBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseInt(v string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return n
	}
	return fallback
}

func parseFloat(v string, fallback float64) float64 {
	if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
		return f
	}
	return fallback
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func clampFloat(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func writeJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(v)
}
