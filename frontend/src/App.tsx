import {ReactNode, useCallback, useEffect, useRef, useState} from 'react'
import {api, errorText, LogLine, Notice, on, Overview, State} from './api'
import {Banner, Confirm, ConfirmRequest, Maple, Spinner} from './components'
import {Configs} from './pages/Configs'
import {Game} from './pages/Game'
import {Home} from './pages/Home'
import {Logs} from './pages/Logs'
import {Settings} from './pages/Settings'

type Page = 'home' | 'configs' | 'game' | 'logs' | 'settings'

const icon = (d: ReactNode) => (
    <svg className="nav-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8"
         strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{d}</svg>
)

const pages: {id: Page, label: string, icon: ReactNode}[] = [
    {id: 'home', label: '加速', icon: icon(<path d="M13 2 4.5 13.5H11L10 22l8.5-11.5H12L13 2z"/>)},
    {id: 'configs', label: '节点配置', icon: icon(<>
        <rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/>
        <path d="M7 7.5h.01M7 16.5h.01"/>
    </>)},
    {id: 'game', label: '游戏与加速项', icon: icon(<>
        <rect x="2.5" y="6" width="19" height="12" rx="6"/><path d="M7 10.5v3M5.5 12h3M15.5 11h.01M17.5 13h.01"/>
    </>)},
    {id: 'logs', label: '日志', icon: icon(<path d="M8 6h13M8 12h13M8 18h13M3.5 6h.01M3.5 12h.01M3.5 18h.01"/>)},
    {id: 'settings', label: '设置与关于', icon: icon(<>
        <path d="M4 21v-7M4 10V3M12 21v-9M12 8V3M20 21v-5M20 12V3M1.5 14h5M9.5 8h5M17.5 16h5"/>
    </>)},
]

const stateLabel: Record<State, string> = {
    idle: '未加速',
    starting: '正在开始',
    running: '加速中',
    stopping: '正在停止',
}

const maxLogLines = 1000

// mergeLogs adds lines not seen yet, keeping sequence order and a size cap.
function mergeLogs(old: LogLine[], lines: LogLine[] | null): LogLine[] {
    const seen = new Set(old.map(l => l.seq))
    const fresh = (lines ?? []).filter(l => !seen.has(l.seq))
    if (fresh.length === 0) return old
    const all = old.concat(fresh)
    if (old.length > 0 && fresh[0].seq < old[old.length - 1].seq) all.sort((a, b) => a.seq - b.seq)
    return all.length > maxLogLines ? all.slice(all.length - maxLogLines) : all
}

export default function App() {
    const [page, setPage] = useState<Page>('home')
    const [overview, setOverview] = useState<Overview | null>(null)
    const [loadError, setLoadError] = useState('')
    const [notices, setNotices] = useState<(Notice & {id: number})[]>([])
    const [confirmRequest, setConfirmRequest] = useState<ConfirmRequest | null>(null)
    const [logs, setLogs] = useState<LogLine[]>([])
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

    const go = (p: Page) => {
        setPage(p)
        refresh()
    }

    const state = overview?.status.state ?? 'idle'
    const current = pages.find(p => p.id === page)!

    let content: ReactNode = null
    if (overview) {
        switch (page) {
            case 'home':
                content = <Home overview={overview} refresh={refresh} confirm={confirm} goConfigs={() => go('configs')}/>
                break
            case 'configs':
                content = <Configs overview={overview} refresh={refresh} confirm={confirm}/>
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
            <aside className="sidebar">
                <div className="brand">
                    <Maple/>
                    <div>
                        <div className="brand-name">MSBOOST</div>
                        <div className="brand-sub">启动器</div>
                    </div>
                </div>
                <div className={'state-pill ' + state}><span className="dot"/>{stateLabel[state]}</div>
                <nav>
                    {pages.map(p => (
                        <button key={p.id} className={'nav-item' + (p.id === page ? ' active' : '')}
                                aria-current={p.id === page ? 'page' : undefined} onClick={() => go(p.id)}>
                            {p.icon}{p.label}
                        </button>
                    ))}
                </nav>
                <div className="sidebar-foot">{overview?.version}</div>
            </aside>

            <main className="content">
                <header className="content-head"><h1>{current.label}</h1></header>
                {notices.map(n => (
                    <Banner key={n.id} kind={n.kind} onClose={() => setNotices(list => list.filter(x => x.id !== n.id))}>{n.text}</Banner>
                ))}
                {loadError && <Banner kind="error">无法读取程序状态：{loadError}</Banner>}
                {overview ? content : !loadError && <div className="loading"><Spinner/>正在加载…</div>}
            </main>

            <Confirm request={confirmRequest} onDone={() => setConfirmRequest(null)}/>
        </div>
    )
}
