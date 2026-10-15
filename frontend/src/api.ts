// Typed access to the Go backend. The generated Wails bindings return class
// instances; they are used here as plain data with the shapes below.
import * as Go from '../wailsjs/go/main/App'
import {EventsOn} from '../wailsjs/runtime/runtime'

export type State = 'idle' | 'starting' | 'running' | 'stopping'

export interface Status {
    state: State
    config: string
    sinceUnix: number
    upload: number // bytes exchanged with the node
    download: number
    uploadRate: number // bytes per second
    downloadRate: number
    systemProxy: string[] | null // system proxy endpoints being taken over
    lastError: string
    addons: Record<string, boolean>
}

export interface ConfigItem {
    name: string
    profile: string
    entrance: string
    error: string
    current: boolean
}

export interface Addon {
    id: string
    name: string
    note: string
    processes: string[]
    enabled: boolean
}

export interface Group {
    name: string
    processes: string[]
}

export interface Conflict {
    message: string
    blocking: boolean
    fix: string // a fix fixConflict can apply, or ""
}

export interface Overview {
    status: Status
    configs: ConfigItem[]
    addons: Addon[]
    gameGroups: Group[]
    probeAddress: string
    probePort: number
    elevated: boolean
    conflicts: Conflict[]
    lockError: string
    settingsError: string
    dataDir: string
    version: string
}

export interface Result {
    text: string
    detail: string
    ok: boolean
    averageMs: number
}

export interface Check {
    exitIP: string
    exitIPError: string
    entranceTarget: string
    entrance: Result
    gameTarget: string
    game: Result
}

export interface LogLine {
    seq: number
    time: string
    tag: string
    text: string
}

export interface Component {
    name: string
    version: string
    copyright: string
    license: string
    url: string
}

export interface Link {
    name: string
    url: string
}

export interface Notice {
    kind: 'error' | 'warn' | 'info'
    text: string
}

export interface About {
    version: string
    copyright: string
    license: string
    components: Component[]
    licenseTexts: Link[]
}

const plain = <T>(v: unknown) => v as T

export const api = {
    overview: async () => plain<Overview>(await Go.Overview()),
    start: async () => (await Go.Start()) ?? [],
    stop: () => Go.Stop(),
    runningGame: async () => (await Go.RunningGameProcesses()) ?? [],
    selectConfig: (name: string) => Go.SelectConfig(name),
    pickConfigFile: () => Go.PickConfigFile(),
    importConfig: async (path: string, overwrite: boolean) =>
        plain<{name: string, exists: boolean}>(await Go.ImportConfig(path, overwrite)),
    deleteConfig: (name: string) => Go.DeleteConfig(name),
    openFolder: (kind: 'configs' | 'logs' | 'data') => Go.OpenFolder(kind),
    setAddon: (id: string, on: boolean) => Go.SetAddon(id, on),
    setProbeTarget: (address: string, port: number) => Go.SetProbeTarget(address, port),
    resetProbeTarget: () => Go.ResetProbeTarget(),
    runCheck: async () => plain<Check>(await Go.RunCheck()),
    recentLogs: async () => plain<LogLine[]>(await Go.RecentLogs()) ?? [],
    exportDiagnostics: () => Go.ExportDiagnostics(),
    cleanup: async () => (await Go.Cleanup()) ?? [],
    fixConflict: async (fix: string) => (await Go.FixConflict(fix)) ?? [],
    about: async () => plain<About>(await Go.About()),
    openURL: (url: string) => Go.OpenURL(url),
    hideToTray: () => Go.HideToTray(),
    quit: () => Go.Quit(),
}

export const on = {
    status: (fn: (s: Status) => void) => EventsOn('status', fn),
    logs: (fn: (lines: LogLine[]) => void) => EventsOn('logs', fn),
    notice: (fn: (n: Notice) => void) => EventsOn('notice', fn),
}

// errorText turns a rejected binding call into a readable message.
export function errorText(err: unknown): string {
    if (typeof err === 'string') return err
    if (err instanceof Error) return err.message
    return String(err)
}
