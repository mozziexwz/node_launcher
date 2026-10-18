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

// The widest text formatBytes and formatUptime produce. Digits render at one
// width (tabular-nums), and M is the widest unit letter.
export const widestBytes = '0000.00 MiB'
export const widestUptime = '000:00:00'

// Steady shows a live value in the width of the widest text it can take, so
// a row of changing numbers keeps its layout instead of reflowing.
export function Steady(props: { value: string, widest: string }) {
    return (
        <b className="steady">
            <span aria-hidden="true">{props.widest}</span>
            <span>{props.value}</span>
        </b>
    )
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
