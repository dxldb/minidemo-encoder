package parser

import (
	"encoding/json"
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
type C4HolderInfo struct {
	RoundNum   int    `json:"round"`
	PlayerName string `json:"player_name"`
}
var outputBaseDir string
var allC4Holders []C4HolderInfo
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
	allC4Holders = make([]C4HolderInfo, 0)

	// 处理特殊event构成的button表示
	var buttonTickMap map[TickPlayer]int32 = make(map[TickPlayer]int32)
	var playerLastScopedState map[uint64]bool = make(map[uint64]bool)
	var (
		roundStarted      = 0
		roundInFreezetime = 0
		roundNum          = 0
	)
	iParser.RegisterEventHandler(func(e events.FrameDone) {
		gs := iParser.GameState()
		currentTick := gs.IngameTick()

		if roundInFreezetime == 0 {
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
					parsePlayerFrame(player, addonButton, iParser.TickRate(), false)
				}
			}
		}
	})

	iParser.RegisterEventHandler(func(e events.WeaponFire) {
		gs := iParser.GameState()
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
		currentTick := gs.IngameTick()
		key := TickPlayer{currentTick, e.Player.SteamID64}
		if _, ok := buttonTickMap[key]; ok {
			buttonTickMap[key] |= IN_JUMP
		} else {
			buttonTickMap[key] = IN_JUMP
		}
	})

	// 包括开局准备时间
	iParser.RegisterEventHandler(func(e events.RoundStart) {
		roundStarted = 1
		roundInFreezetime = 1
	})

	// 准备时间结束，正式开始
	iParser.RegisterEventHandler(func(e events.RoundFreezetimeEnd) {
		roundInFreezetime = 0
		roundNum += 1
		ilog.InfoLogger.Println("回合开始：", roundNum)
		// 初始化录像文件
		// 写入所有选手的初始位置和角度
		gs := iParser.GameState()
		tPlayers := gs.TeamTerrorists().Members()
		ctPlayers := gs.TeamCounterTerrorists().Members()
		Players := append(tPlayers, ctPlayers...)
		for _, player := range Players {
			if player != nil {
				// parse player
				parsePlayerInitFrame(player)
			}
		}
		detectC4Holder(&gs, roundNum)
	})

	// 回合结束，不包括自由活动时间
	iParser.RegisterEventHandler(func(e events.RoundEnd) {
		if roundStarted == 0 {
			roundStarted = 1
			roundNum = 0
		}
		ilog.InfoLogger.Println("回合结束：", roundNum)
		// 结束录像文件
		gs := iParser.GameState()
		tPlayers := gs.TeamTerrorists().Members()
		ctPlayers := gs.TeamCounterTerrorists().Members()
		Players := append(tPlayers, ctPlayers...)
		for _, player := range Players {
			if player != nil {
				// save to rec file
				saveToRecFile(player, int32(roundNum))
			}
		}
	})
	err = iParser.ParseToEnd()
	checkError(err)
}
	
func detectC4Holder(gs *dem.GameState, roundNum int) {
	tPlayers := (*gs).TeamTerrorists().Members()

	for _, player := range tPlayers {
		if player == nil {
			continue
		}

		for _, weapon := range player.Weapons() {
			if weapon != nil && weapon.Type == common.EqBomb {
				c4Info := C4HolderInfo{
					RoundNum:   roundNum,
					PlayerName: player.Name,
				}
				allC4Holders = append(allC4Holders, c4Info)

				ilog.InfoLogger.Printf("  [C4检测] 回合 %d: %s 持有C4",
					roundNum, player.Name)
				return
			}
		}
	}

	ilog.InfoLogger.Printf("  [C4检测] ⚠ 回合 %d: 未检测到C4持有者", roundNum)
}

func saveC4HolderData() error {
	c4File := filepath.Join(outputBaseDir, "c4_holders.json")

	data, err := json.MarshalIndent(allC4Holders, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(c4File, data, 0644)
}
