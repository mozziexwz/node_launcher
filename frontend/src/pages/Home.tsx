import {useState} from 'react'
import {api, Check, errorText, Overview} from '../api'
import {Banner, Card, formatBytes, formatUptime, Spinner, Steady, Switch, useClock, widestBytes, widestUptime} from '../components'

const fixLabels: Record<string, string> = {
    proxybridge: '停止 ProxyBridge',
}

const stateText = {
    idle: '未加速',
    starting: '正在开始加速…',
    running: '加速中',
    stopping: '正在停止加速…',
}

export function Home(props: {
    overview: Overview
    refresh: () => Promise<void>
    confirm: (title: string, message: string, confirm: string, danger?: boolean) => Promise<boolean>
    goConfigs: () => void
}) {
    const o = props.overview
    const st = o.status
    const busy = st.state === 'starting' || st.state === 'stopping'
    const running = st.state === 'running'
    const validConfigs = o.configs.filter(c => !c.error)
    const blocking = o.conflicts.filter(c => c.blocking)
    const [warnings, setWarnings] = useState<string[]>([])
    const [error, setError] = useState('')
    const [check, setCheck] = useState<Check | null>(null)
    const [checking, setChecking] = useState(false)
    const [checkError, setCheckError] = useState('')
    const [fixing, setFixing] = useState('')
    const [fixReport, setFixReport] = useState<string[]>([])
    useClock(running)

    const hasCurrent = validConfigs.some(c => c.current)
    const canStart = !busy && o.elevated && !o.lockError && hasCurrent && blocking.length === 0

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

    const runCheck = async () => {
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
    }

    return (
        <div className="page">
            {o.lockError && <Banner kind="error">{o.lockError}</Banner>}
            {!o.elevated && <Banner kind="error">程序没有以管理员身份运行，无法开始加速。请右键程序选择“以管理员身份运行”。</Banner>}
            {o.settingsError && <Banner kind="warn">{o.settingsError}</Banner>}
            {o.conflicts.map(c => (
                <Banner key={c.message} kind={c.blocking ? 'error' : 'warn'} action={fixAction(c.fix)}>{c.message}</Banner>
            ))}
            {fixReport.length > 0 && <Banner kind="info" onClose={() => setFixReport([])}>{fixReport.join('；')}</Banner>}
            {error && <Banner kind="error" onClose={() => setError('')}>{error}</Banner>}
            {warnings.map(w => <Banner key={w} kind="warn" onClose={() => setWarnings(ws => ws.filter(x => x !== w))}>{w}</Banner>)}

            <section className={'hero ' + st.state}>
                <div className="hero-state">
                    <div className="hero-label">{stateText[st.state]}</div>
                    <div className="hero-sub">
                        {st.state === 'running' ? (
                                <>
                                    <div className="hero-stats">
                                        <span>已运行 <Steady value={formatUptime(st.sinceUnix)} widest={widestUptime}/></span>
                                        <span>已使用流量 <Steady value={formatBytes(st.upload + st.download)} widest={widestBytes}/></span>
                                        <span>当前速率 <Steady value={`↑ ${formatBytes(st.uploadRate)}/s`} widest={`↑ ${widestBytes}/s`}/>
                                            {' '}<Steady value={`↓ ${formatBytes(st.downloadRate)}/s`} widest={`↓ ${widestBytes}/s`}/></span>
                                    </div>
                                    {st.systemProxy && st.systemProxy.length > 0 && (
                                        <div className="hero-note">
                                            已接管系统代理 {st.systemProxy.join('、')}：游戏发往它的请求改由 MSBOOST 发出
                                        </div>
                                    )}
                                </>
                            )
                            : st.state === 'starting' ? '正在连接 MSBOOST 节点并加载分流驱动…'
                            : st.state === 'stopping' ? '正在断开连接并卸载分流驱动…'
                            : validConfigs.length === 0 ? <>还没有可用的节点配置，请先<button className="link" onClick={props.goConfigs}>导入配置</button></>
                            : !hasCurrent ? '请先在下方选择节点配置'
                            : '开始加速后再打开 MapleStory'}
                    </div>
                </div>
                <button className={'hero-button ' + (running ? 'stop' : 'start')} disabled={running ? busy : !canStart} onClick={toggle}>
                    {busy && <Spinner/>}
                    {running ? '停止加速' : st.state === 'starting' ? '正在开始…' : '开始加速'}
                </button>
            </section>

            <div className="grid two">
                <Card title="节点配置" actions={<button className="link" onClick={props.goConfigs}>管理配置</button>}>
                    {validConfigs.length === 0
                        ? <p className="muted">configs 文件夹中没有可用的配置。</p>
                        : (
                            <select value={validConfigs.find(c => c.current)?.name ?? ''} disabled={st.state !== 'idle'}
                                    onChange={e => select(e.target.value)}>
                                {!hasCurrent && <option value="" disabled>请选择节点配置</option>}
                                {validConfigs.map(c => <option key={c.name} value={c.name}>{c.name.replace(/\.json$/i, '')}</option>)}
                            </select>
                        )}
                    {st.state !== 'idle' && <p className="muted small">加速中不能切换配置，请先停止加速。</p>}
                </Card>

                <Card title="可选加速">
                    {o.addons.map(a => (
                        <div className="addon-row" key={a.id}>
                            <div>
                                <div className="addon-name">{a.name}</div>
                                <div className="muted small">{a.note}</div>
                            </div>
                            <Switch label={a.name} checked={a.enabled} disabled={busy} onChange={v => switchAddon(a.id, v)}/>
                        </div>
                    ))}
                    {running && <p className="muted small">加速中切换只对之后新建的连接生效。</p>}
                </Card>
            </div>

            <Card title="网络检测" actions={
                <button onClick={runCheck} disabled={checking || busy || !hasCurrent}>
                    {checking && <Spinner/>}{checking ? '检测中…' : '开始检测'}
                </button>
            }>
                {checkError && <Banner kind="error">{checkError}</Banner>}
                {!check && !checkError && (
                    <p className="muted">查询连接到游戏服务器的 IPv4，并检测到中转服务器及到游戏服务器的延迟。</p>
                )}
                {check && (
                    <dl className="check">
                        <dt>当前到游戏服务器 IPv4</dt>
                        <dd className={check.exitIP ? 'ok' : 'bad'}>{check.exitIP || '查询失败：' + check.exitIPError}</dd>
                        <dt>当前到中转服务器延迟</dt>
                        <dd className={check.entrance.ok ? 'ok' : 'bad'}>
                            {check.entrance.text}
                            {check.entranceTarget && <span className="target"> {check.entranceTarget}　{check.entrance.detail}</span>}
                        </dd>
                        <dt>当前到游戏服务器延迟</dt>
                        <dd className={check.game.ok ? 'ok' : 'bad'}>
                            {check.game.text}
                            <span className="target"> {check.gameTarget}　{check.game.detail}</span>
                        </dd>
                    </dl>
                )}
            </Card>
        </div>
    )
}
