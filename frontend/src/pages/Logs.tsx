import {useEffect, useMemo, useRef, useState} from 'react'
import {api, errorText, LogLine} from '../api'
import {Banner, Card, Spinner} from '../components'

const tags = ['全部', '程序', '核心', '分流', '连接']
const tagClass: Record<string, string> = {'程序': 'app', '核心': 'core', '分流': 'redirect', '连接': 'conn'}

export function Logs(props: {lines: LogLine[]}) {
    const [tag, setTag] = useState('全部')
    const [follow, setFollow] = useState(true)
    const [message, setMessage] = useState<{kind: 'error' | 'info', text: string} | null>(null)
    const [exporting, setExporting] = useState(false)
    const box = useRef<HTMLDivElement>(null)
    const shown = useMemo(() => tag === '全部' ? props.lines : props.lines.filter(l => l.tag === tag), [props.lines, tag])

    useEffect(() => {
        if (follow && box.current) box.current.scrollTop = box.current.scrollHeight
    }, [shown, follow])

    const onScroll = () => {
        const el = box.current
        if (el) setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 24)
    }

    // Exporting checks the game's DNS lookups, which can take a few seconds.
    const exportDiagnostics = async () => {
        setMessage(null)
        setExporting(true)
        try {
            const path = await api.exportDiagnostics()
            if (path) setMessage({kind: 'info', text: '诊断包已保存到 ' + path})
        } catch (e) {
            setMessage({kind: 'error', text: errorText(e)})
        } finally {
            setExporting(false)
        }
    }

    return (
        <div className="page fill">
            <Card className="fill" title="运行日志" actions={<>
                <div className="segmented" role="group" aria-label="日志类型">
                    {tags.map(t => <button key={t} className={t === tag ? 'on' : ''} onClick={() => setTag(t)}>{t}</button>)}
                </div>
                <button onClick={() => api.openFolder('logs')}>打开日志文件夹</button>
                <button onClick={exportDiagnostics} disabled={exporting}>{exporting && <Spinner/>}导出诊断包…</button>
            </>}>
                <p className="muted small">
                    日志按天保存 7 天。日志和诊断包里有节点地址，只发给 MSBOOST 客服，不要公开发布。
                </p>
                {message && <Banner kind={message.kind} onClose={() => setMessage(null)}>{message.text}</Banner>}
                <div className="log-wrap">
                    <div className="log-box" ref={box} onScroll={onScroll}>
                        {shown.length === 0 && <div className="muted">暂无日志</div>}
                        {shown.map(l => (
                            <div className="log-line" key={l.seq}>
                                <span className="log-time">{l.time}</span>
                                <span className={'log-tag ' + (tagClass[l.tag] ?? '')}>{l.tag}</span>
                                <span className="log-text">{l.text}</span>
                            </div>
                        ))}
                    </div>
                    {!follow && <button className="follow" onClick={() => setFollow(true)}>回到最新 ↓</button>}
                </div>
            </Card>
        </div>
    )
}
