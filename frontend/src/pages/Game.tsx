import {useEffect, useState} from 'react'
import {api, errorText, Overview} from '../api'
import {Banner, Card, Switch} from '../components'

export function Game(props: {overview: Overview, refresh: () => Promise<void>}) {
    const o = props.overview
    const busy = o.status.state === 'starting' || o.status.state === 'stopping'
    const [address, setAddress] = useState(o.probeAddress)
    const [port, setPort] = useState(String(o.probePort))
    const [message, setMessage] = useState<{kind: 'error' | 'info', text: string} | null>(null)

    useEffect(() => {
        setAddress(o.probeAddress)
        setPort(String(o.probePort))
    }, [o.probeAddress, o.probePort])

    const save = async () => {
        setMessage(null)
        const p = Number(port)
        if (!Number.isInteger(p)) {
            setMessage({kind: 'error', text: '端口必须是 1–65535 之间的整数'})
            return
        }
        try {
            await api.setProbeTarget(address, p)
            setMessage({kind: 'info', text: '已保存'})
        } catch (e) {
            setMessage({kind: 'error', text: errorText(e)})
        }
        await props.refresh()
    }

    const reset = async () => {
        setMessage(null)
        try {
            await api.resetProbeTarget()
            setMessage({kind: 'info', text: '已恢复默认'})
        } catch (e) {
            setMessage({kind: 'error', text: errorText(e)})
        }
        await props.refresh()
    }

    const switchAddon = async (id: string, on: boolean) => {
        try {
            await api.setAddon(id, on)
        } catch (e) {
            setMessage({kind: 'error', text: errorText(e)})
        }
        await props.refresh()
    }

    return (
        <div className="page">
            <Card title="加速对象">
                <p className="muted">以下程序的 TCP 和 UDP 连接会经过 MSBOOST 节点，其他程序一律直连。清单固定，随新版本更新。</p>
                {o.gameGroups.map(g => (
                    <div className="group" key={g.name}>
                        <div className="group-name">{g.name}</div>
                        <div className="chips">{g.processes.map(p => <span className="chip" key={p}>{p}</span>)}</div>
                    </div>
                ))}
            </Card>

            <Card title="可选加速">
                {o.addons.map(a => (
                    <div className="addon-row" key={a.id}>
                        <div>
                            <div className="addon-name">{a.name}</div>
                            <div className="muted small">{a.note}</div>
                            <div className="chips">{a.processes.map(p => <span className="chip" key={p}>{p}</span>)}</div>
                        </div>
                        <Switch label={a.name} checked={a.enabled} disabled={busy} onChange={v => switchAddon(a.id, v)}/>
                    </div>
                ))}
            </Card>

            <Card title="全程测速目标">
                <p className="muted">
                    “到游戏服务器延迟”会经节点连接这个地址。请填写游戏服务器的公网 IPv4 和端口，
                    例如默认的 35.155.204.207:8585；玩 MapleLegends 可以填 51.222.56.192:8484。
                </p>
                {message && <Banner kind={message.kind} onClose={() => setMessage(null)}>{message.text}</Banner>}
                <div className="target-form">
                    <label>IPv4<input value={address} onChange={e => setAddress(e.target.value)} spellCheck={false}/></label>
                    <label>端口<input value={port} onChange={e => setPort(e.target.value)} inputMode="numeric" className="port"/></label>
                    <button className="primary" onClick={save}>保存</button>
                    <button onClick={reset}>恢复默认</button>
                </div>
            </Card>
        </div>
    )
}
