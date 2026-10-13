package main

import (
	"fmt"
	"strings"

	"github.com/mozziexwz/node_launcher/internal/app"
	"github.com/mozziexwz/node_launcher/internal/logx"
	"github.com/mozziexwz/node_launcher/internal/probe"
	"github.com/mozziexwz/node_launcher/internal/profiles"
	"github.com/mozziexwz/node_launcher/internal/store"
)

// Values sent to the frontend. Times are Unix milliseconds; 0 means unset.

type StatusDTO struct {
	State     string          `json:"state"` // idle | starting | running | stopping
	Config    string          `json:"config"`
	SinceUnix int64           `json:"sinceUnix"`
	Accepted  int64           `json:"accepted"`
	Active    int64           `json:"active"`
	LastError string          `json:"lastError"`
	Addons    map[string]bool `json:"addons"`
}

type ConfigDTO struct {
	Name     string `json:"name"`
	Profile  string `json:"profile"`
	Entrance string `json:"entrance"`
	Error    string `json:"error"`
	Current  bool   `json:"current"`
}

type AddonDTO struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Note      string   `json:"note"`
	Processes []string `json:"processes"`
	Enabled   bool     `json:"enabled"`
}

type GroupDTO struct {
	Name      string   `json:"name"`
	Processes []string `json:"processes"`
}

type ConflictDTO struct {
	Message  string `json:"message"`
	Blocking bool   `json:"blocking"`
}

type OverviewDTO struct {
	Status        StatusDTO     `json:"status"`
	Configs       []ConfigDTO   `json:"configs"`
	Addons        []AddonDTO    `json:"addons"`
	GameGroups    []GroupDTO    `json:"gameGroups"`
	ProbeAddress  string        `json:"probeAddress"`
	ProbePort     int           `json:"probePort"`
	Elevated      bool          `json:"elevated"`
	Conflicts     []ConflictDTO `json:"conflicts"`
	LockError     string        `json:"lockError"`
	SettingsError string        `json:"settingsError"`
	DataDir       string        `json:"dataDir"`
	Version       string        `json:"version"`
}

type ResultDTO struct {
	Text      string  `json:"text"`
	Detail    string  `json:"detail"`
	OK        bool    `json:"ok"`
	AverageMs float64 `json:"averageMs"`
}

type CheckDTO struct {
	ExitIP         string    `json:"exitIP"`
	ExitIPError    string    `json:"exitIPError"`
	EntranceTarget string    `json:"entranceTarget"`
	Entrance       ResultDTO `json:"entrance"`
	GameTarget     string    `json:"gameTarget"`
	Game           ResultDTO `json:"game"`
}

type ImportResultDTO struct {
	Name   string `json:"name"`
	Exists bool   `json:"exists"` // a different file with this name is already in configs\
}

type LegacyResultDTO struct {
	Imported []string `json:"imported"`
	Skipped  []string `json:"skipped"`
	Current  string   `json:"current"`
}

type LogLineDTO struct {
	Seq  uint64 `json:"seq"`
	Time string `json:"time"`
	Tag  string `json:"tag"`
	Text string `json:"text"`
}

type ComponentDTO struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Copyright string `json:"copyright"`
	License   string `json:"license"`
	URL       string `json:"url"`
}

type LinkDTO struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type AboutDTO struct {
	Version      string         `json:"version"`
	Copyright    string         `json:"copyright"`
	License      string         `json:"license"`
	Components   []ComponentDTO `json:"components"`
	LicenseTexts []LinkDTO      `json:"licenseTexts"`
}

func toStatusDTO(st app.Status) StatusDTO {
	dto := StatusDTO{
		State: string(st.State), Config: st.Config, Accepted: st.Accepted, Active: st.Active,
		LastError: st.LastError, Addons: st.Addons,
	}
	if !st.Since.IsZero() {
		dto.SinceUnix = st.Since.UnixMilli()
	}
	return dto
}

func toConfigDTOs(list []store.ConfigEntry, current string) []ConfigDTO {
	out := make([]ConfigDTO, 0, len(list))
	for _, c := range list {
		dto := ConfigDTO{Name: c.Name, Profile: c.Profile, Entrance: c.Entrance, Current: c.Name == current}
		if c.Err != nil {
			dto.Error = c.Err.Error()
		}
		out = append(out, dto)
	}
	return out
}

func toAddonDTOs(enabled map[string]bool) []AddonDTO {
	var out []AddonDTO
	for _, a := range profiles.Addons() {
		out = append(out, AddonDTO{ID: a.ID, Name: a.Name, Note: a.Note, Processes: a.Processes, Enabled: enabled[a.ID]})
	}
	return out
}

func toGroupDTOs() []GroupDTO {
	var out []GroupDTO
	for _, g := range profiles.DisplayGroups() {
		out = append(out, GroupDTO{Name: g.Name, Processes: g.Processes})
	}
	return out
}

func toConflictDTOs(list []app.Conflict) []ConflictDTO {
	out := make([]ConflictDTO, 0, len(list))
	for _, c := range list {
		out = append(out, ConflictDTO{Message: c.Message, Blocking: c.Blocking})
	}
	return out
}

// toResultDTO shows the latency once: Text has it (or why it failed) and
// Detail only adds the success count and any partial failures.
func toResultDTO(r probe.Result) ResultDTO {
	detail := fmt.Sprintf("成功 %d/%d", r.SuccessCount, r.TotalCount)
	if r.SuccessCount > 0 && r.SuccessCount < r.TotalCount {
		detail += "；部分失败：" + strings.Join(r.Errors, "、")
	}
	return ResultDTO{
		Text: r.Compact(), Detail: detail, OK: r.SuccessCount > 0,
		AverageMs: float64(r.Average.Microseconds()) / 1000,
	}
}

func toCheckDTO(r app.CheckResult) CheckDTO {
	return CheckDTO{
		ExitIP: r.ExitIP, ExitIPError: r.ExitIPError,
		EntranceTarget: r.EntranceTarget, Entrance: toResultDTO(r.Entrance),
		GameTarget: r.GameTarget, Game: toResultDTO(r.Game),
	}
}

func toLogLineDTO(l logx.Line) LogLineDTO {
	return LogLineDTO{Seq: l.Seq, Time: l.Time.Format("15:04:05"), Tag: string(l.Tag), Text: l.Text}
}
