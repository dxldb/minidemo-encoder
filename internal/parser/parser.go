package parser

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dxldb/minidemo-encoder/internal/encoder"
	ilog "github.com/dxldb/minidemo-encoder/internal/logger"
	dem "github.com/markus-wa/demoinfocs-golang/v2/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v2/pkg/demoinfocs/common"
	events "github.com/markus-wa/demoinfocs-golang/v2/pkg/demoinfocs/events"
)

type TickPlayer struct {
	tick    int
	steamid uint64
}

type RoundInfo struct {
	roundNum           int
	freezetimeStart    int
	freezetimeEnd      int
	roundEnd           int
	inFreezeTime       bool
	isHalftime         bool
	started            bool
}

var outputBaseDir string

// 帧率检测相关变量
var (
	detectedTickRate  float64 = 128.0
	detectedFrameRate float64 = 128.0
	timeScaleFactor   float64 = 1.0
	frameRateDetected bool    = false
	shouldInterpolate bool    = false
)
// 检测demo的实际帧率
func detectActualFrameRate(parser dem.Parser) {
	var (
		frameSamples  []float64
		lastFrameTick int
		sampleCount   int
		maxSamples    = 500
	)

	parser.RegisterEventHandler(func(e events.FrameDone) {
		gs := parser.GameState()

		if gs.IsWarmupPeriod() || frameRateDetected {
			return
		}

		currentTick := gs.IngameTick()

		if lastFrameTick > 0 {
			tickDiff := currentTick - lastFrameTick
			if tickDiff > 0 && tickDiff < 10 {
				frameSamples = append(frameSamples, float64(tickDiff))
				sampleCount++
			}
		}

		lastFrameTick = currentTick

		if sampleCount >= maxSamples {
			frameRateDetected = true

			var sum float64
			for _, sample := range frameSamples {
				sum += sample
			}
			avgTicksPerFrame := sum / float64(len(frameSamples))

			detectedTickRate = parser.TickRate()
			detectedFrameRate = detectedTickRate / avgTicksPerFrame
			timeScaleFactor = detectedFrameRate / detectedTickRate

			ilog.InfoLogger.Printf("========== 帧率检测结果 ==========")
			ilog.InfoLogger.Printf("Demo Tick Rate: %.2f", detectedTickRate)
			ilog.InfoLogger.Printf("实际帧率: %.2f fps", detectedFrameRate)
			ilog.InfoLogger.Printf("平均每帧间隔: %.2f ticks", avgTicksPerFrame)
			ilog.InfoLogger.Printf("时间缩放因子: %.4f", timeScaleFactor)

			// 智能判断是否需要插帧
			const MIN_ACCEPTABLE_FPS = 90.0 // 最低可接受帧率
			const MAX_NORMAL_FPS = 120.0    // 正常帧率上限

			if detectedFrameRate < MIN_ACCEPTABLE_FPS {
				shouldInterpolate = true
				targetFPS := 128.0
				interpolationRatio := targetFPS / detectedFrameRate
				ilog.InfoLogger.Printf("  检测到低帧率 (%.1f fps)，将进行插帧优化", detectedFrameRate)
				ilog.InfoLogger.Printf("  目标帧率: %.0f fps (插值比例: %.2fx)", targetFPS, interpolationRatio)
			} else if detectedFrameRate > MAX_NORMAL_FPS {
				shouldInterpolate = false
				ilog.InfoLogger.Printf("  高帧率demo (%.1f fps)，无需处理", detectedFrameRate)
			} else {
				shouldInterpolate = false
				ilog.InfoLogger.Printf("  帧率正常 (%.1f fps)，无需处理", detectedFrameRate)
			}

			ilog.InfoLogger.Printf("==================================")
			saveDemoInfo()
		}
	})
}

func getAdjustedTime(tickDiff int, tickRate float64) float64 {
	return float64(tickDiff) / tickRate
}

// 保存demo信息到文件
func saveDemoInfo() {
	infoFile := filepath.Join(outputBaseDir, "demo_info.json")

	processingInfo := "无需处理"
	if shouldInterpolate {
		targetFPS := 128.0
		interpolationRatio := targetFPS / detectedFrameRate
		processingInfo = fmt.Sprintf("已进行插帧优化 (%.1f fps → %.0f fps, %.2fx)",
			detectedFrameRate, targetFPS, interpolationRatio)
	}

	info := map[string]interface{}{
		"tick_rate":           detectedTickRate,
		"original_frame_rate": detectedFrameRate,
		"target_frame_rate":   128.0,
		"interpolated":        shouldInterpolate,
		"interpolation_ratio": 128.0 / detectedFrameRate,
		"note":                processingInfo,
	}

	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		ilog.ErrorLogger.Printf("保存demo信息失败: %s\n", err.Error())
		return
	}

	os.WriteFile(infoFile, data, 0644)
	ilog.InfoLogger.Printf("Demo信息已保存到: %s", infoFile)
}

func Start(filePath string) {
	iFile, err := os.Open(filePath)
	checkError(err)

	iParser := dem.NewParser(iFile)
	defer iParser.Close()

	demoFileName := filepath.Base(filePath)
	demoName := strings.TrimSuffix(demoFileName, filepath.Ext(demoFileName))

	outputBaseDir = filepath.Join("output", demoName)

	err = os.MkdirAll(outputBaseDir, os.ModePerm)
	if err != nil {
		ilog.ErrorLogger.Printf("创建输出目录失败: %s\n", err.Error())
		return
	}

	encoder.SetSaveDir(outputBaseDir)

	ilog.InfoLogger.Printf("输出目录: %s", outputBaseDir)

	var buttonTickMap map[TickPlayer]int32 = make(map[TickPlayer]int32)
	detectActualFrameRate(iParser)
	var playerLastScopedState map[uint64]bool = make(map[uint64]bool)
	var (
		roundNum           = 0
		currentRound       *RoundInfo
		firstRoundDetected = false
		gameStarted        = false
	)

	iParser.RegisterEventHandler(func(e events.FrameDone) {
		gs := iParser.GameState()

		// 检查是否在热身
		if gs.IsWarmupPeriod() {
			return
		}

		// 检查游戏是否已开始
		if !gameStarted || currentRound == nil || !currentRound.started {
			return
		}

		currentTick := gs.IngameTick()

		tPlayers := gs.TeamTerrorists().Members()
		ctPlayers := gs.TeamCounterTerrorists().Members()
		Players := append(tPlayers, ctPlayers...)
		for _, player := range Players {
			if player != nil {
				var addonButton int32 = 0
				key := TickPlayer{currentTick, player.SteamID64}
				if val, ok := buttonTickMap[key]; ok {
					addonButton = val
					delete(buttonTickMap, key)
				}

				steamID := player.SteamID64
				currentScoped := player.IsScoped()
				lastScoped, exists := playerLastScopedState[steamID]

				if !exists {
					playerLastScopedState[steamID] = currentScoped
				} else if currentScoped != lastScoped {
					addonButton |= IN_ATTACK2
					playerLastScopedState[steamID] = currentScoped
				}

				parsePlayerFrame(player, addonButton, iParser.TickRate(), currentRound.inFreezeTime)
			}
		}
	})

	iParser.RegisterEventHandler(func(e events.WeaponFire) {
		gs := iParser.GameState()

		// 检查是否在热身
		if gs.IsWarmupPeriod() {
			return
		}

		currentTick := gs.IngameTick()
		key := TickPlayer{currentTick, e.Shooter.SteamID64}
		if _, ok := buttonTickMap[key]; ok {
			buttonTickMap[key] |= IN_ATTACK
		} else {
			buttonTickMap[key] = IN_ATTACK
		}
	})

	iParser.RegisterEventHandler(func(e events.PlayerJump) {
		gs := iParser.GameState()

		// 检查是否在热身
		if gs.IsWarmupPeriod() {
			return
		}

		currentTick := gs.IngameTick()
		key := TickPlayer{currentTick, e.Player.SteamID64}
		if _, ok := buttonTickMap[key]; ok {
			buttonTickMap[key] |= IN_JUMP
		} else {
			buttonTickMap[key] = IN_JUMP
		}
	})

	iParser.RegisterEventHandler(func(e events.GameHalfEnded) {
		gs := iParser.GameState()

		// 检查是否在热身
		if gs.IsWarmupPeriod() {
			return
		}

		if currentRound != nil {
			currentRound.isHalftime = true
			ilog.InfoLogger.Printf("半场结束,回合 %d 包含换边时间", currentRound.roundNum)
		}
	})

	iParser.RegisterEventHandler(func(e events.RoundStart) {
		gs := iParser.GameState()
		currentTick := gs.IngameTick()

		// 检查热身状态
		if gs.IsWarmupPeriod() {
			ilog.InfoLogger.Printf("跳过热身回合 (Tick: %d)", currentTick)
			if gameStarted {
				ilog.InfoLogger.Printf("⚠ 检测到重新进入热身，重置游戏状态")
				gameStarted = false
				firstRoundDetected = false
				roundNum = 0
				currentRound = nil
			}
			return
		}

		// 如果当前回合还存在且已开始，不处理新的 RoundStart
		if currentRound != nil && currentRound.started {
			ilog.InfoLogger.Printf("⚠ 检测到重复的 RoundStart 事件 (Tick: %d)，当前回合 %d 还未结束，跳过", currentTick, currentRound.roundNum)
			return
		}

		if !firstRoundDetected {
			firstRoundDetected = true
			gameStarted = true
			roundNum = 1
			ilog.InfoLogger.Printf("检测到第一个正式回合开始")
		} else {
			roundNum++
		}

		if currentRound != nil && currentRound.roundNum == roundNum {
			ilog.InfoLogger.Printf("回合 %d 已初始化,跳过重复初始化", roundNum)
			return
		}

		ilog.InfoLogger.Printf("====================================")
		ilog.InfoLogger.Printf("回合 %d 开始 (Tick: %d)", roundNum, currentTick)

		currentRound = &RoundInfo{
			roundNum:        roundNum,
			freezetimeStart: currentTick,
			freezetimeEnd:   currentTick,
			inFreezeTime:    true,
			isHalftime:      false,
			started:         false,
		}

		playerLastScopedState = make(map[uint64]bool)

		currentRound.started = true
	})

	iParser.RegisterEventHandler(func(e events.RoundFreezetimeEnd) {
		gs := iParser.GameState()

		// 检查是否在热身
		if gs.IsWarmupPeriod() {
			return
		}

		if currentRound != nil {
			currentTick := gs.IngameTick()

			currentRound.freezetimeEnd = currentTick
			currentRound.inFreezeTime = false
			tPlayers := gs.TeamTerrorists().Members()
			ctPlayers := gs.TeamCounterTerrorists().Members()
			Players := append(tPlayers, ctPlayers...)
	
			for _, player := range Players {
				if player != nil {
					parsePlayerInitFrame(player)
				}
			}
		}
	})

	iParser.RegisterEventHandler(func(e events.RoundEnd) {
		gs := iParser.GameState()

		if gs.IsWarmupPeriod() {
			ilog.InfoLogger.Printf("⚠  回合结束时检测到热身状态，跳过保存")
			currentRound = nil
			return
		}

		if !gameStarted {
			ilog.InfoLogger.Printf("⚠  游戏未开始，跳过回合结束处理")
			currentRound = nil
			return
		}

		if currentRound != nil {
			currentTick := gs.IngameTick()
			currentRound.roundEnd = currentTick

			tPlayers := gs.TeamTerrorists().Members()
			ctPlayers := gs.TeamCounterTerrorists().Members()
			Players := append(tPlayers, ctPlayers...)

			ilog.InfoLogger.Printf("  正在保存录像文件...")
			savedCount := 0

			for _, player := range Players {
				if player != nil {
					// 如果需要插帧，先进行插帧
					if shouldInterpolate {
						interpolatePlayerFrames(player.Name)
					}
					saveToRecFile(player, int32(currentRound.roundNum))
					savedCount++
				}
			}

			if shouldInterpolate {
				ilog.InfoLogger.Printf("  ✓ 已对所有玩家进行插帧优化")
			}

			ilog.InfoLogger.Printf("  已保存 %d 个玩家录像到: %s/round%d/", savedCount, outputBaseDir, currentRound.roundNum)
			ilog.InfoLogger.Printf("====================================\n")

			currentRound = nil
		}
	})

	err = iParser.ParseToEnd()
	checkError(err)

	ilog.InfoLogger.Printf("\n解析完成!所有回合录像已保存到 %s/ 目录", outputBaseDir)
	ilog.InfoLogger.Printf("共解析 %d 个回合\n", roundNum)
}
