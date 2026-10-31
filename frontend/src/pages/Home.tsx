import {useState} from 'react'
import {api, errorText, Overview, Transport} from '../api'
import {Banner, Spinner, Switch} from '../components'
import {canCheck, CheckState, checkLabel, shortResult} from './Check'

const fixLabels: Record<string, string> = {
    proxybridge: '停止 ProxyBridge',
}

const stateText = {
    idle: '未加速',
    starting: '正在开始加速…',
    running: '加速中',
    stopping: '正在停止加速…',
}

// CheckSummary is the one-line form of the network check on the home page.
function CheckSummary(props: {check: CheckState}) {
    const {result: r, running, error} = props.check
    if (running) return <span className="summary none"><Spinner/>正在检测 MSBOOST IPv4 和延迟…</span>
    if (error) return <span className="summary"><b className="bad">检测失败</b></span>
    if (!r) return <span className="summary none"><span>MSBOOST IPv4 —</span><span>中转 —</span><span>游戏 —</span></span>
    return (
        <span className="summary">
            <span>MSBOOST IPv4 <b className={r.exitIP ? '' : 'bad'}>{r.exitIP || '查询失败'}</b></span>
            <span>中转 <b className={r.entrance.ok ? 'ok' : 'bad'}>{shortResult(r.entrance)}</b></span>
            <span>游戏 <b className={r.game.ok ? 'ok' : 'bad'}>{shortResult(r.game)}</b></span>
        </span>
    )
}

export function Home(props: {
    overview: Overview
    refresh: () => Promise<void>
    confirm: (title: string, message: string, confirm: string, danger?: boolean) => Promise<boolean>
    check: CheckState
    goConfigs: () => void
    goCheck: () => void
}) {
    const o = props.overview
    const st = o.status
    const busy = st.state === 'starting' || st.state === 'stopping'
    const running = st.state === 'running'
    const validConfigs = o.configs.filter(c => !c.error)
    const blocking = o.conflicts.filter(c => c.blocking)
    const [warnings, setWarnings] = useState<string[]>([])
    const [error, setError] = useState('')
    const [fixing, setFixing] = useState('')
    const [fixReport, setFixReport] = useState<string[]>([])

    const hasCurrent = validConfigs.some(c => c.current)
    const canStart = !busy && o.elevated && !o.lockError && hasCurrent && blocking.length === 0
    // Only a node that serves UDP offers the choice. While accelerating the
    // control shows what is in use, which is TCP after a UDP fallback.
    const offersUDP = validConfigs.some(c => c.current && c.udp)
    const shownTransport: Transport = st.state === 'idle' || !st.transport ? o.transport : st.transport

    const toggle = async () => {
        setError('')
        try {
            if (running) {
                const game = await api.runningGame()
                if (game.length > 0 && !await props.confirm('停止加速',
                    `MapleStory 正在运行（${game.join('、')}），停止加速会断开游戏连接。确定停止吗？`, '停止加速', true)) {
                    return
                }
                await api.stop()
            } else {
                setWarnings(await api.start())
            }
        } catch (e) {
            setError(errorText(e))
        } finally {
            await props.refresh()
        }
    }

    const select = async (name: string) => {
        setError('')
        try {
            await api.selectConfig(name)
        } catch (e) {
            setError(errorText(e))
        }
        await props.refresh()
    }

    const chooseTransport = async (t: Transport) => {
        setError('')
        try {
            await api.setTransport(t)
        } catch (e) {
            setError(errorText(e))
        }
        await props.refresh()
    }

    const switchAddon = async (id: string, on: boolean) => {
        setError('')
        try {
            await api.setAddon(id, on)
        } catch (e) {
            setError(errorText(e))
        }
        await props.refresh()
    }

    const fixConflict = async (fix: string) => {
        if (!await props.confirm('停止 ProxyBridge',
            'MSBOOST 会让 ProxyBridge 退出（和它托盘菜单里的“退出”一样），并停止它的后台服务（ProxyBridgeSvc）。' +
            '如果它设置了开机启动，下次开机还会运行；不再使用的话，建议卸载 ProxyBridge。确定继续吗？', '停止', true)) {
            return
        }
        setFixing(fix)
        setError('')
        setFixReport([])
        try {
            setFixReport(await api.fixConflict(fix))
        } catch (e) {
            setError(errorText(e))
        } finally {
            setFixing('')
            await props.refresh()
        }
    }

    // One button per fix, even when two conflicts share it.
    const fixShown = new Set<string>()
    const fixAction = (fix: string) => {
        if (!fix || fixShown.has(fix) || busy) return undefined
        fixShown.add(fix)
        return {label: fixLabels[fix] ?? '自动处理', onClick: () => fixConflict(fix), busy: fixing === fix}
    }

    const note = running ? [
            st.transport === 'udp' ? '经 UDP 连接节点' : '',
            st.systemProxy && st.systemProxy.length > 0
                ? `已接管系统代理 ${st.systemProxy.join('、')}：游戏发往它的请求改由 MSBOOST 发出` : '',
        ].filter(Boolean).join('；')
        : st.state === 'starting' ? '正在连接 MSBOOST 节点并加载分流驱动…'
        : st.state === 'stopping' ? '正在断开连接并卸载分流驱动…'
        : !hasCurrent ? '请先在上方选择节点配置'
        : '开始加速后再打开 MapleStory'

    return (
        <div className="page home">
            {o.lockError && <Banner kind="error">{o.lockError}</Banner>}
            {!o.elevated && <Banner kind="error">程序没有以管理员身份运行，无法开始加速。请右键程序选择“以管理员身份运行”。</Banner>}
            {o.settingsError && <Banner kind="warn">{o.settingsError}</Banner>}
            {o.conflicts.map(c => (
                <Banner key={c.message} kind={c.blocking ? 'error' : 'warn'} action={fixAction(c.fix)}>{c.message}</Banner>
            ))}
            {fixReport.length > 0 && <Banner kind="info" onClose={() => setFixReport([])}>{fixReport.join('；')}</Banner>}
            {error && <Banner kind="error" onClose={() => setError('')}>{error}</Banner>}
            {warnings.map(w => <Banner key={w} kind="warn" onClose={() => setWarnings(ws => ws.filter(x => x !== w))}>{w}</Banner>)}
            {props.check.result && !props.check.result.dns.ok && (
                <Banner kind="warn" action={{label: '详情', onClick: props.goCheck}}>
                    本机 DNS 解析不了游戏域名，游戏可能登录不了。
                </Banner>
            )}

            <section className="lines">
                <div className="line">
                    <span className="line-label">节点配置</span>
                    {validConfigs.length === 0
                        ? <span className="muted">还没有可用的节点配置，请先<button className="link" onClick={props.goConfigs}>导入配置</button></span>
                        : (
                            <select value={validConfigs.find(c => c.current)?.name ?? ''} disabled={st.state !== 'idle'}
                                    title={st.state !== 'idle' ? '加速中不能切换配置，请先停止加速' : undefined}
                                    onChange={e => select(e.target.value)}>
                                {!hasCurrent && <option value="" disabled>请选择节点配置</option>}
                                {validConfigs.map(c => <option key={c.name} value={c.name}>{c.name.replace(/\.json$/i, '')}</option>)}
                            </select>
                        )}
                    <span className="line-actions">
                        {offersUDP && (
                            <div className="segmented compact" role="group" aria-label="连接方式"
                                 title={st.state !== 'idle' ? '加速中不能切换连接方式，请先停止加速' : 'UDP 连不上节点时会自动改用 TCP'}>
                                {(['tcp', 'udp'] as Transport[]).map(t => (
                                    <button key={t} className={t === shownTransport ? 'on' : ''} disabled={st.state !== 'idle'}
                                            onClick={() => t !== o.transport && chooseTransport(t)}>{t.toUpperCase()}</button>
                                ))}
                            </div>
                        )}
                        <button className="link" onClick={props.goConfigs}>管理</button>
                    </span>
                </div>
                <div className="line">
                    <span className="line-label">可选加速</span>
                    <div className="toggles">
                        {o.addons.map(a => (
                            <span className="toggle" key={a.id} title={a.note}>
                                <Switch label={a.name} checked={a.enabled} disabled={busy} onChange={v => switchAddon(a.id, v)}/>
                                {a.name}
                            </span>
                        ))}
                    </div>
                    <span className="hint">{running ? '加速中切换只对新连接生效' : ''}</span>
                </div>
                <div className="line">
                    <span className="line-label">网络检测</span>
                    <CheckSummary check={props.check}/>
                    <span className="line-actions">
                        <button className="small" onClick={props.check.run} disabled={!canCheck(o, props.check)}>{checkLabel(props.check)}</button>
                        <button className="link" onClick={props.goCheck}>详情</button>
                    </span>
                </div>
            </section>

            <section className="home-foot">
                <div className="state-block">
                    <div className={'state-title ' + st.state}><span className="dot"/>{stateText[st.state]}</div>
                    {note && <div className="state-note">{note}</div>}
                </div>
                <button className={'hero-button ' + (running ? 'stop' : 'start')} disabled={running ? busy : !canStart} onClick={toggle}>
                    {busy && <Spinner/>}
                    {running ? '停止加速' : st.state === 'starting' ? '正在开始…' : st.state === 'stopping' ? '正在停止…' : '开始加速'}
                </button>
            </section>
        </div>
    )
}
