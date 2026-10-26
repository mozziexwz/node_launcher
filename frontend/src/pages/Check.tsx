import {Check, Overview, Result} from '../api'
import {Banner, Card, Spinner} from '../components'

// CheckState is the network check shared by the home summary and this page.
export interface CheckState {
    result: Check | null
    running: boolean
    error: string
    run: () => void
}

// canCheck reports whether a check may start: it needs a current config and
// no start or stop in progress.
export function canCheck(o: Overview, c: CheckState) {
    const busy = o.status.state === 'starting' || o.status.state === 'stopping'
    return !c.running && !busy && o.configs.some(x => x.current && !x.error)
}

export function checkLabel(c: CheckState) {
    return c.running ? '检测中' : c.result || c.error ? '重新检测' : '开始检测'
}

// shortResult is the home summary's form of a latency: the value, or why it
// failed in one word.
export function shortResult(r: Result) {
    if (r.ok) return r.text
    return r.text.includes('超时') ? '超时' : '失败'
}

export function CheckPage(props: {overview: Overview, check: CheckState}) {
    const o = props.overview
    const c = props.check
    const r = c.result
    const hasCurrent = o.configs.some(x => x.current && !x.error)
    const pending = <span className="none">{c.running ? '检测中…' : '—'}</span>

    return (
        <div className="page">
            <Card title="网络检测" actions={
                <button className="primary" onClick={c.run} disabled={!canCheck(o, c)}>
                    {c.running && <Spinner/>}{checkLabel(c)}
                </button>
            }>
                <p className="muted small">
                    查询连接到游戏服务器的 IPv4，并检测到中转服务器及到游戏服务器的延迟。游戏测速目标可在“游戏与加速项”中修改。
                </p>
                {!hasCurrent && <Banner kind="warn">请先在“加速”页选择节点配置。</Banner>}
                {c.error && <Banner kind="error">{c.error}</Banner>}
                <dl className="check">
                    <dt>当前到游戏服务器 IPv4</dt>
                    <dd className={r ? (r.exitIP ? 'ok' : 'bad') : ''}>
                        {r ? r.exitIP || '查询失败：' + r.exitIPError : pending}
                    </dd>
                    <dt>当前到中转服务器延迟</dt>
                    <dd className={r ? (r.entrance.ok ? 'ok' : 'bad') : ''}>
                        {r ? <>
                            {r.entrance.text}
                            {r.entranceTarget && <span className="target">{r.entranceTarget}　{r.entrance.detail}</span>}
                        </> : pending}
                    </dd>
                    <dt>当前到游戏服务器延迟</dt>
                    <dd className={r ? (r.game.ok ? 'ok' : 'bad') : ''}>
                        {r ? <>
                            {r.game.text}
                            <span className="target">{r.gameTarget}　{r.game.detail}</span>
                        </> : pending}
                    </dd>
                    <dt>本机 DNS 解析游戏域名</dt>
                    <dd className={r ? (r.dns.ok ? 'ok' : 'bad') : ''} title={r ? r.dns.failed.join('\n') : undefined}>
                        {r ? <>
                            {r.dns.text}
                            {r.dns.servers.length > 0 && <span className="target">DNS 服务器 {r.dns.servers.join('、')}</span>}
                        </> : pending}
                    </dd>
                </dl>
                {r && !r.dns.ok && <Banner kind="warn">{r.dns.advice}</Banner>}
            </Card>
        </div>
    )
}
