import {useState} from 'react'
import {api, errorText, Overview} from '../api'
import {Banner, Card} from '../components'

export function Configs(props: {
    overview: Overview
    refresh: () => Promise<void>
    confirm: (title: string, message: string, confirm: string, danger?: boolean) => Promise<boolean>
}) {
    const o = props.overview
    const idle = o.status.state === 'idle'
    const [message, setMessage] = useState<{kind: 'error' | 'info', text: string} | null>(null)

    const run = async (fn: () => Promise<void>) => {
        setMessage(null)
        try {
            await fn()
        } catch (e) {
            setMessage({kind: 'error', text: errorText(e)})
        }
        await props.refresh()
    }

    const importFile = () => run(async () => {
        const path = await api.pickConfigFile()
        if (!path) return
        let result = await api.importConfig(path, false)
        if (result.exists) {
            if (!await props.confirm('替换配置', `configs 文件夹中已有同名但内容不同的“${result.name}”。要用新文件替换吗？`, '替换', true)) return
            result = await api.importConfig(path, true)
        }
        setMessage({kind: 'info', text: `已导入 ${result.name}`})
    })

    const remove = (name: string) => run(async () => {
        if (!await props.confirm('删除配置', `确定从 configs 文件夹删除“${name}”吗？`, '删除', true)) return
        await api.deleteConfig(name)
    })

    return (
        <div className="page">
            <Card title="节点配置" actions={<>
                <button className="primary" onClick={importFile}>导入配置文件…</button>
                <button onClick={() => api.openFolder('configs')}>打开配置文件夹</button>
                <button onClick={() => props.refresh()}>刷新</button>
            </>}>
                <p className="muted">
                    在网站下载的配置文件（.json）可以直接放进程序旁边的 configs 文件夹，然后点“刷新”；也可以用“导入配置文件”。
                </p>
                {message && <Banner kind={message.kind} onClose={() => setMessage(null)}>{message.text}</Banner>}
                {o.configs.length === 0 && <p className="empty">还没有节点配置。</p>}
                <ul className="config-list">
                    {o.configs.map(c => (
                        <li key={c.name} className={(c.current ? 'current ' : '') + (c.error ? 'invalid' : '')}>
                            <div className="config-main">
                                <div className="config-name">
                                    {c.name.replace(/\.json$/i, '')}
                                    {c.current && <span className="badge">当前</span>}
                                </div>
                                {c.error
                                    ? <div className="bad small">无效：{c.error}</div>
                                    : <div className="muted small">入口 {c.entrance}</div>}
                            </div>
                            <div className="config-actions">
                                {!c.error && !c.current && (
                                    <button disabled={!idle} onClick={() => run(() => api.selectConfig(c.name))}>设为当前</button>
                                )}
                                <button className="danger-text" disabled={!idle && c.current} onClick={() => remove(c.name)}>删除</button>
                            </div>
                        </li>
                    ))}
                </ul>
                {!idle && <p className="muted small">加速中不能切换配置。</p>}
            </Card>
        </div>
    )
}
