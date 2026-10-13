import {useEffect, useState} from 'react'
import {About, api, errorText, Overview} from '../api'
import {Banner, Card, Spinner} from '../components'

export function Settings(props: {
    overview: Overview
    refresh: () => Promise<void>
    confirm: (title: string, message: string, confirm: string, danger?: boolean) => Promise<boolean>
}) {
    const o = props.overview
    const idle = o.status.state === 'idle'
    const [about, setAbout] = useState<About | null>(null)
    const [report, setReport] = useState<string[] | null>(null)
    const [cleaning, setCleaning] = useState(false)
    const [error, setError] = useState('')

    useEffect(() => {
        api.about().then(setAbout).catch(e => setError(errorText(e)))
    }, [])

    const cleanup = async () => {
        if (!await props.confirm('清理残留',
            '将卸载分流驱动，并删除 MSBOOST 添加的防火墙规则和运行时文件。节点配置和设置不受影响。确定继续吗？', '清理', true)) {
            return
        }
        setCleaning(true)
        setError('')
        setReport(null)
        try {
            setReport(await api.cleanup())
        } catch (e) {
            setError(errorText(e))
        } finally {
            setCleaning(false)
        }
        await props.refresh()
    }

    const quit = async () => {
        if (!idle && !await props.confirm('退出', '正在加速，退出会停止加速并断开游戏连接。确定退出吗？', '退出', true)) {
            return
        }
        await api.quit()
    }

    return (
        <div className="page">
            {error && <Banner kind="error" onClose={() => setError('')}>{error}</Banner>}

            <Card title="程序">
                <dl className="facts">
                    <dt>数据目录</dt>
                    <dd>
                        <span className="path">{o.dataDir}</span>
                        <button className="link" onClick={() => api.openFolder('data')}>打开</button>
                    </dd>
                    <dt>关闭窗口</dt>
                    <dd>点窗口右上角的 × 会最小化到托盘，加速不受影响。要退出请用托盘菜单或下面的“退出程序”。</dd>
                </dl>
                <div className="row-actions">
                    <button onClick={() => api.hideToTray()}>最小化到托盘</button>
                    <button className="danger" onClick={quit}>退出程序</button>
                </div>
            </Card>

            <Card title="清理残留" actions={
                <button onClick={cleanup} disabled={!idle || cleaning}>
                    {cleaning && <Spinner/>}{cleaning ? '清理中…' : '清理'}
                </button>
            }>
                <p className="muted">
                    程序停止加速时会自动卸载驱动。如果程序曾经异常退出，或者打算删除本程序，可以先在这里清理。
                    {!idle && '加速中不能清理，请先停止加速。'}
                </p>
                {report && (
                    <ul className="report">
                        {report.map((line, i) => <li key={i}>{line}</li>)}
                    </ul>
                )}
            </Card>

            <Card title="关于">
                {about ? (
                    <>
                        <dl className="facts">
                            <dt>版本</dt>
                            <dd>{about.version}</dd>
                            <dt>源代码</dt>
                            <dd><button className="link" onClick={() => api.openURL(about.source)}>{about.source}</button></dd>
                        </dl>
                        <p className="notice">{about.license}</p>
                        <table className="components">
                            <thead>
                                <tr><th>使用的开源组件</th><th>版本</th><th>许可</th></tr>
                            </thead>
                            <tbody>
                                {about.components.map(c => (
                                    <tr key={c.name}>
                                        <td><button className="link" onClick={() => api.openURL(c.url)}>{c.name}</button></td>
                                        <td>{c.version || '—'}</td>
                                        <td>{c.license}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </>
                ) : <p className="muted"><Spinner/>正在读取…</p>}
            </Card>
        </div>
    )
}
