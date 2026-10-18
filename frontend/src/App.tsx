import {ReactNode, useCallback, useEffect, useRef, useState} from 'react'
import {api, Check, errorText, LogLine, Notice, on, Overview, State} from './api'
import {Banner, Confirm, ConfirmRequest, formatBytes, formatUptime, Spinner, Steady, useClock, widestBytes, widestUptime} from './components'
import {CheckPage, CheckState} from './pages/Check'
import {Configs} from './pages/Configs'
import {Game} from './pages/Game'
import {Home} from './pages/Home'
import {Logs} from './pages/Logs'
import {Settings} from './pages/Settings'

type Page = 'home' | 'configs' | 'check' | 'game' | 'logs' | 'settings'

const pages: {id: Page, label: string}[] = [
    {id: 'home', label: '加速'},
    {id: 'configs', label: '节点配置'},
    {id: 'check', label: '网络检测'},
    {id: 'game', label: '游戏与加速项'},
    {id: 'logs', label: '日志'},
    {id: 'settings', label: '设置与关于'},
]

const bolt = (
    <svg className="tab-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8"
         strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z"/>
    </svg>
)

const statusText: Record<State, string> = {
    idle: '未加速',
    starting: '正在开始加速…',
    running: '加速中',
    stopping: '正在停止加速…',
}

const maxLogLines = 1000
const homepage = 'https://msboost.de/'

// mergeLogs adds lines not seen yet, keeping sequence order and a size cap.
function mergeLogs(old: LogLine[], lines: LogLine[] | null): LogLine[] {
    const seen = new Set(old.map(l => l.seq))
    const fresh = (lines ?? []).filter(l => !seen.has(l.seq))
    if (fresh.length === 0) return old
    const all = old.concat(fresh)
    if (old.length > 0 && fresh[0].seq < old[old.length - 1].seq) all.sort((a, b) => a.seq - b.seq)
    return all.length > maxLogLines ? all.slice(all.length - maxLogLines) : all
}

// StatusBar is the bottom strip, shown on every page: the session's uptime,
// traffic and rates while accelerating, otherwise the state.
function StatusBar(props: {overview: Overview | null}) {
    const st = props.overview?.status
    useClock(st?.state === 'running')
    const current = props.overview?.configs.find(c => c.current)
    const config = (st?.config || current?.name || '').replace(/\.json$/i, '')
    let left: ReactNode = <span>正在加载…</span>
    if (st?.state === 'running') {
        left = <>
            <span>已运行 <Steady value={formatUptime(st.sinceUnix)} widest={widestUptime}/></span>
            <span>已使用流量 <Steady value={formatBytes(st.upload + st.download)} widest={widestBytes}/></span>
            <span>↑ <Steady value={`${formatBytes(st.uploadRate)}/s`} widest={`${widestBytes}/s`}/>
                {' '}↓ <Steady value={`${formatBytes(st.downloadRate)}/s`} widest={`${widestBytes}/s`}/></span>
        </>
    } else if (st) {
        left = <span>{statusText[st.state]}</span>
    }
    return (
        <footer className="statusbar">
            <div className="status-left">{left}</div>
            <div className="status-right">{config}</div>
        </footer>
    )
}

export default function App() {
    const [page, setPage] = useState<Page>('home')
    const [overview, setOverview] = useState<Overview | null>(null)
    const [loadError, setLoadError] = useState('')
    const [notices, setNotices] = useState<(Notice & {id: number})[]>([])
    const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null)
    const [logs, setLogs] = useState<LogLine[]>([])
    const [check, setCheck] = useState<Check | null>(null)
    const [checking, setChecking] = useState(false)
    const [checkError, setCheckError] = useState('')
    const lastState = useRef<State | null>(null)
    const noticeId = useRef(0)
    const refreshing = useRef<Promise<void> | null>(null)
    const refreshAgain = useRef(false)

    // refresh reloads the overview. Calls made while one is running are merged
    // into a single follow-up, so the backend never runs its checks in
    // parallel (startup alone triggers a load and a window focus).
    const refresh = useCallback((): Promise<void> => {
        if (refreshing.current) {
            refreshAgain.current = true
            return refreshing.current
        }
        const run = async () => {
            do {
                refreshAgain.current = false
                try {
                    const o = await api.overview()
                    lastState.current = o.status.state
                    setOverview(o)
                    setLoadError('')
                } catch (e) {
                    setLoadError(errorText(e))
                }
            } while (refreshAgain.current)
            refreshing.current = null
        }
        refreshing.current = run()
        return refreshing.current
    }, [])

    useEffect(() => {
        refresh()
        const offStatus = on.status(s => {
            setOverview(o => o && {...o, status: s})
            if (s.state !== lastState.current) {
                lastState.current = s.state
                refresh() // conflicts and the config list may change with the state
            }
        })
        const offNotice = on.notice(n => setNotices(list => [...list, {...n, id: ++noticeId.current}]))
        const onFocus = () => refresh()
        window.addEventListener('focus', onFocus)
        return () => {
            offStatus()
            offNotice()
            window.removeEventListener('focus', onFocus)
        }
    }, [refresh])

    // Subscribe before reading the history so no line is missed; sequence
    // numbers remove the overlap.
    useEffect(() => {
        const off = on.logs(lines => setLogs(old => mergeLogs(old, lines)))
        api.recentLogs().then(lines => setLogs(old => mergeLogs(old, lines)))
        return off
    }, [])

    const confirm = useCallback((title: string, message: string, confirmLabel: string, danger = false) =>
        new Promise<boolean>(resolve => setConfirmRequest({title, message, confirm: confirmLabel, danger, resolve})), [])

    // The network check lives here so the home summary and the check page
    // show the same result.
    const runCheck = useCallback(async () => {
        setChecking(true)
        setCheckError('')
        setCheck(null)
        try {
            setCheck(await api.runCheck())
        } catch (e) {
            setCheckError(errorText(e))
        } finally {
            setChecking(false)
        }
    }, [])
    const checkState: CheckState = {result: check, running: checking, error: checkError, run: runCheck}

    const go = (p: Page) => {
        setPage(p)
        refresh()
    }

    let content: ReactNode = null
    if (overview) {
        switch (page) {
            case 'home':
                content = <Home overview={overview} refresh={refresh} confirm={confirm} check={checkState}
                                goConfigs={() => go('configs')} goCheck={() => go('check')}/>
                break
            case 'configs':
                content = <Configs overview={overview} refresh={refresh} confirm={confirm}/>
                break
            case 'check':
                content = <CheckPage overview={overview} check={checkState}/>
                break
            case 'game':
                content = <Game overview={overview} refresh={refresh}/>
                break
            case 'logs':
                content = <Logs lines={logs}/>
                break
            case 'settings':
                content = <Settings overview={overview} refresh={refresh} confirm={confirm}/>
                break
        }
    }

    return (
        <div className="shell">
            <nav className="tabs">
                {pages.map(p => (
                    <button key={p.id} className={'tab' + (p.id === page ? ' active' : '')}
                            aria-current={p.id === page ? 'page' : undefined} onClick={() => go(p.id)}>
                        {p.id === 'home' && bolt}{p.label}
                    </button>
                ))}
                <div className="tabs-right">
                    <span>{overview?.version}</span>
                    <button className="link home-link" onClick={() => api.openURL(homepage)}>MSBOOST</button>
                </div>
            </nav>

            <main className="content">
                {notices.map(n => (
                    <Banner key={n.id} kind={n.kind} onClose={() => setNotices(list => list.filter(x => x.id !== n.id))}>{n.text}</Banner>
                ))}
                {loadError && <Banner kind="error">无法读取程序状态：{loadError}</Banner>}
                {overview ? content : !loadError && <div className="loading"><Spinner/>正在加载…</div>}
            </main>

            <StatusBar overview={overview}/>
            <Confirm request={confirmRequest} onDone={() => setConfirmRequest(null)}/>
        </div>
    )
}
