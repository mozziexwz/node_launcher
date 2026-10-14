import {ReactNode, useEffect, useState} from 'react'

export function Card(props: {title?: ReactNode, actions?: ReactNode, children: ReactNode, className?: string}) {
    return (
        <section className={'card ' + (props.className ?? '')}>
            {(props.title || props.actions) && (
                <header className="card-head">
                    <h2>{props.title}</h2>
                    <div className="card-actions">{props.actions}</div>
                </header>
            )}
            {props.children}
        </section>
    )
}

export function Banner(props: {
    kind: 'error' | 'warn' | 'info'
    children: ReactNode
    onClose?: () => void
    action?: {label: string, onClick: () => void, busy?: boolean}
}) {
    const a = props.action
    return (
        <div className={'banner ' + props.kind} role={props.kind === 'error' ? 'alert' : 'status'}>
            <div className="banner-text">{props.children}</div>
            {a && (
                <button className="banner-action" disabled={a.busy} onClick={a.onClick}>
                    {a.busy && <Spinner/>}{a.label}
                </button>
            )}
            {props.onClose && <button className="link" onClick={props.onClose}>知道了</button>}
        </div>
    )
}

export function Switch(props: {checked: boolean, disabled?: boolean, onChange: (v: boolean) => void, label: string}) {
    return (
        <label className={'switch' + (props.disabled ? ' disabled' : '')}>
            <input type="checkbox" checked={props.checked} disabled={props.disabled}
                   onChange={e => props.onChange(e.target.checked)} aria-label={props.label}/>
            <span className="track"><span className="thumb"/></span>
        </label>
    )
}

export interface ConfirmRequest {
    title: string
    message: ReactNode
    confirm: string
    danger?: boolean
    resolve: (ok: boolean) => void
}

// Confirm renders the pending confirmation, if any, as a modal dialog.
export function Confirm(props: {request: ConfirmRequest | null, onDone: () => void}) {
    const r = props.request
    useEffect(() => {
        if (!r) return
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                r.resolve(false)
                props.onDone()
            }
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [r, props])
    if (!r) return null
    const close = (ok: boolean) => {
        r.resolve(ok)
        props.onDone()
    }
    return (
        <div className="modal-backdrop" onClick={() => close(false)}>
            <div className="modal" role="dialog" aria-modal="true" onClick={e => e.stopPropagation()}>
                <h3>{r.title}</h3>
                <div className="modal-body">{r.message}</div>
                <div className="modal-actions">
                    {/* Enter must not confirm a destructive action by accident. */}
                    <button autoFocus={r.danger} onClick={() => close(false)}>取消</button>
                    <button className={r.danger ? 'danger' : 'primary'} autoFocus={!r.danger} onClick={() => close(true)}>{r.confirm}</button>
                </div>
            </div>
        </div>
    )
}

// Modal shows content in a dialog that Escape, the backdrop or 关闭 dismisses.
export function Modal(props: {title: string, onClose: () => void, children: ReactNode, wide?: boolean}) {
    const {onClose} = props
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') onClose()
        }
        window.addEventListener('keydown', onKey)
        return () => window.removeEventListener('keydown', onKey)
    }, [onClose])
    return (
        <div className="modal-backdrop" onClick={onClose}>
            <div className={'modal' + (props.wide ? ' wide' : '')} role="dialog" aria-modal="true" aria-label={props.title}
                 onClick={e => e.stopPropagation()}>
                <h3>{props.title}</h3>
                <div className="modal-body">{props.children}</div>
                <div className="modal-actions">
                    <button className="primary" autoFocus onClick={onClose}>关闭</button>
                </div>
            </div>
        </div>
    )
}

// Maple is the MSBOOST mark, the same drawing as the website's.
export function Maple(props: {size?: number}) {
    const size = props.size ?? 30
    return (
        <svg className="maple" viewBox="0 0 64 64" fill="none" width={size} height={size} aria-hidden="true">
            <path d="M5 31a27 27 0 0 0 54 0" stroke="#000" strokeWidth="2.5" strokeLinecap="round"/>
            <path d="M204 24C197 49 182 83 168 78C160 98 157 124 132 118C142 144 154 190 141 198C126 204 95 187 79 174C66 189 44 197 21 193C38 213 49 230 46 254C64 254 94 262 101 277C111 293 88 317 80 330C105 327 129 336 146 346C165 344 191 328 201 318C201 339 199 360 191 378C186 391 200 393 204 384C211 364 213 339 210 319C225 332 245 341 264 346C284 335 309 329 330 330C315 312 303 294 307 279C312 262 338 254 361 253C358 234 369 210 384 193C363 194 342 187 331 174C308 187 276 205 265 198C253 191 268 143 276 119C256 126 245 96 241 78C225 85 211 49 204 24Z"
                  transform="translate(9 3) scale(.115)" fill="#fff" stroke="#000" strokeWidth="17.4" strokeLinejoin="round"/>
        </svg>
    )
}

export function Spinner() {
    return <span className="spinner" aria-hidden="true"/>
}

// useClock re-renders every second so uptimes stay current.
export function useClock(active: boolean) {
    const [, setTick] = useState(0)
    useEffect(() => {
        if (!active) return
        const id = window.setInterval(() => setTick(t => t + 1), 1000)
        return () => window.clearInterval(id)
    }, [active])
}

const byteUnits = ['B', 'KiB', 'MiB', 'GiB', 'TiB']

// formatBytes matches Netch: binary units with at most two decimals.
export function formatBytes(n: number): string {
    let v = n
    let i = 0
    while (v >= 1024 && i < byteUnits.length - 1) {
        v /= 1024
        i++
    }
    return `${i === 0 ? v : Number(v.toFixed(2))} ${byteUnits[i]}`
}

export function formatUptime(sinceUnix: number): string {
    if (!sinceUnix) return '—'
    const total = Math.max(0, Math.floor((Date.now() - sinceUnix) / 1000))
    const h = Math.floor(total / 3600)
    const m = Math.floor((total % 3600) / 60)
    const s = total % 60
    const two = (n: number) => n.toString().padStart(2, '0')
    return `${two(h)}:${two(m)}:${two(s)}`
}
