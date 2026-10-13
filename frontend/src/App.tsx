import {useEffect, useState} from 'react'
import {HideToTray, PickConfig, Status, ToggleAcceleration} from '../wailsjs/go/main/App'
import {main} from '../wailsjs/go/models'
import {EventsOn} from '../wailsjs/runtime/runtime'

// M0 shell: validates an elevated Wails window, the file dialog and the
// tray. Acceleration is simulated until M2 connects the real engine.
export default function App() {
    const [status, setStatus] = useState<main.Status | null>(null)
    const [config, setConfig] = useState<main.ConfigInfo | null>(null)
    const [message, setMessage] = useState('')

    useEffect(() => {
        Status().then(setStatus)
        return EventsOn('status', (s: main.Status) => setStatus(s))
    }, [])

    const pick = async () => {
        setMessage('')
        try {
            setConfig(await PickConfig())
        } catch (err) {
            setMessage(String(err))
        }
    }

    const toggle = async () => setStatus(await ToggleAcceleration())

    return (
        <main className="page">
            <header>
                <h1>MSBOOST 启动器</h1>
                <span className="badge">M0 界面验证</span>
            </header>

            <section className="card">
                <div className="row">
                    <span className="label">管理员权限</span>
                    <span className={status?.elevated ? 'ok' : 'bad'}>
                        {status == null ? '…' : status.elevated ? '是' : '否（WinDivert 需要管理员）'}
                    </span>
                </div>
                <div className="row">
                    <span className="label">加速状态</span>
                    <span className={status?.accelerating ? 'ok' : ''}>
                        {status?.accelerating ? '加速中' : '未开始'}（模拟）
                    </span>
                </div>
                <div className="actions">
                    <button className="primary" onClick={toggle}>
                        {status?.accelerating ? '停止加速' : '开始加速'}
                    </button>
                    <button onClick={() => HideToTray()}>隐藏到托盘</button>
                </div>
            </section>

            <section className="card">
                <div className="row">
                    <span className="label">节点配置</span>
                    <button onClick={pick}>选择配置文件…</button>
                </div>
                {config && (
                    <dl>
                        <dt>文件</dt><dd>{config.path}</dd>
                        <dt>方案</dt><dd>{config.profile}</dd>
                        <dt>入口</dt><dd>{config.entrance}</dd>
                    </dl>
                )}
                {message && <p className="bad">{message}</p>}
            </section>

            <p className="hint">关闭窗口会隐藏到托盘；在托盘图标右键菜单中选择“退出”才会真正退出。</p>
        </main>
    )
}
