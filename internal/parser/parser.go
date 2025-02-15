package parser

import (
	"math"
	"os"

	ilog "github.com/dxldb/minidemo-encoder/internal/logger"
	dem "github.com/markus-wa/demoinfocs-golang/v3/pkg/demoinfocs"
	events "github.com/markus-wa/demoinfocs-golang/v3/pkg/demoinfocs/events"
)

type TickPlayer struct {
	tick    int
	steamid uint64
}

func Start(filePath string) {
	iFile, err := os.Open(filePath)
	checkError(err)

	iParser := dem.NewParser(iFile)
	defer iParser.Close()

	// 处理特殊event构成的button表示
	var buttonTickMap = make(map[TickPlayer]int32)

	var connectedPlayerMap = make(map[uint64]int32)
	var roundstart = false
	var matchstart = false
	var roundNum = 0
	var realTick = 0
	iParserHeader, err := iParser.ParseHeader()
	if err == nil {
		ilog.InfoLogger.Printf("demo实际Tick为：%d", int(math.Floor(iParserHeader.FrameRate()+0.5)))
		ilog.InfoLogger.Printf("demo演示地图为: %s", iParserHeader.MapName)
		realTick = int(math.Floor(iParserHeader.FrameRate() + 0.5))
		ilog.InfoLogger.Println(iParserHeader.FrameRate())
	}

	iParser.RegisterEventHandler(func(e events.FrameDone) {
		gs := iParser.GameState()
		currentTick := gs.IngameTick()

		if roundstart && matchstart {
			tPlayers := gs.TeamTerrorists().Members()
			ctPlayers := gs.TeamCounterTerrorists().Members()
			Players := append(tPlayers, ctPlayers...)
			for _, player := range Players {
				if player != nil && player.IsAlive() {
					var addonButton int32 = 0
					key := TickPlayer{currentTick, player.SteamID64}
					if val, ok := buttonTickMap[key]; ok {
						addonButton = val
						delete(buttonTickMap, key)
					}
					parsePlayerFrame(player, addonButton, roundNum, iParser.TickRate(), false)
				}
			}
		}
	})

	iParser.RegisterEventHandler(func(e events.MatchStartedChanged) {
		if e.NewIsStarted && !matchstart {
			matchstart = true
		}
	})

	iParser.RegisterEventHandler(func(e events.AnnouncementWinPanelMatch) {
		if matchstart {
			matchstart = false
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


	// 准备时间结束，正式开始
	iParser.RegisterEventHandler(func(e events.RoundStart) {
		roundstart = true
		roundNum++
		ilog.InfoLogger.Printf("回合开始: %d tick: %d", roundNum, iParser.GameState().IngameTick())
		// 初始化录像文件
		gs := iParser.GameState()
		tPlayers := gs.TeamTerrorists().Members()
		ctPlayers := gs.TeamCounterTerrorists().Members()
		Players := append(tPlayers, ctPlayers...)
		for _, player := range Players {
			if player != nil {
				// parse player
				parsePlayerInitFrame(player, realTick)
			}
		}
	})


	iParser.RegisterEventHandler(func(e events.RoundEnd) {
		if matchstart {
			roundstart = false
			ilog.InfoLogger.Printf("回合结束: %d tick: %d", roundNum, iParser.GameState().IngameTick())
			// 结束录像文件
			gs := iParser.GameState()
			tPlayers := gs.TeamTerrorists().Members()
			ctPlayers := gs.TeamCounterTerrorists().Members()
			Players := append(tPlayers, ctPlayers...)
			for _, player := range Players {
				if player != nil {
					saveToRecFile(player, int32(roundNum))
				}
			}
		}
	})


	iParser.RegisterEventHandler(func(e events.PlayerConnect) {
		if e.Player != nil {
			connectedPlayerMap[e.Player.SteamID64] = int32(e.Player.EntityID - 1)
		}
	})
	err = iParser.ParseToEnd()
	checkError(err)
}
