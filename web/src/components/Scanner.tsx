// Point the camera at a barcode. Where the browser can read barcodes
// (Android, desktop Chrome) it reads them itself; elsewhere (iPhone) a
// frame goes to the server every moment. A photo or typing it in always works.
import { useEffect, useRef, useState } from 'react'
import { sendFile, V1 } from '../lib/api'
import { browserDetector, detectIn, shrinkImage } from '../lib/wallet'
import { Icon } from './icons'
import { Modal, Spinner } from './ui'

export interface Found { format: string; value: string }

export function Scanner({ onFound, onClose }: { onFound: (f: Found) => void; onClose: () => void }) {
  const video = useRef<HTMLVideoElement>(null)
  const [state, setState] = useState<'starting' | 'live' | 'nocam'>('starting')
  const [why, setWhy] = useState('')
  const [busy, setBusy] = useState(false)
  const [miss, setMiss] = useState('')
  const done = useRef(false)

  useEffect(() => {
    let stream: MediaStream | null = null
    let timer = 0
    let alive = true
    const finish = (f: Found) => { if (done.current) return; done.current = true; navigator.vibrate?.(40); onFound(f) }
    const start = async () => {
      if (!navigator.mediaDevices?.getUserMedia) { setState('nocam'); setWhy('This browser cannot use the camera here.'); return }
      try {
        stream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }, audio: false })
      } catch (e) {
        setState('nocam')
        setWhy((e as Error).name === 'NotAllowedError' ? 'The camera is not allowed. Allow it in the browser, or open Wallet in its own tab (a page inside another can be refused the camera).' : 'No camera could be opened.')
        return
      }
      if (!alive || !video.current) { stream.getTracks().forEach(t => t.stop()); return }
      video.current.srcObject = stream
      await video.current.play().catch(() => {})
      setState('live')
      const local = await browserDetector()
      const cv = document.createElement('canvas')
      const tick = async () => {
        if (!alive || done.current || !video.current) return
        const v = video.current
        if (v.videoWidth) {
          if (local) {
            const f = await detectIn(v)
            if (f) { finish(f); return }
          } else {
            const scale = Math.min(1, 1280 / Math.max(v.videoWidth, v.videoHeight))
            cv.width = Math.round(v.videoWidth * scale); cv.height = Math.round(v.videoHeight * scale)
            cv.getContext('2d')!.drawImage(v, 0, 0, cv.width, cv.height)
            const blob = await new Promise<Blob | null>(ok => cv.toBlob(ok, 'image/jpeg', 0.8))
            if (blob) {
              const r = await sendFile<{ found: Found | null }>('POST', `${V1}/scan`, blob).catch(() => null)
              if (r?.found) { finish(r.found); return }
            }
          }
        }
        timer = window.setTimeout(tick, local ? 250 : 700)
      }
      tick()
    }
    start()
    return () => { alive = false; clearTimeout(timer); stream?.getTracks().forEach(t => t.stop()) }
  }, [onFound])

  const photo = async (file: File) => {
    setBusy(true); setMiss('')
    try {
      const blob = await shrinkImage(file, 2000, 0.9)
      const bmp = await createImageBitmap(blob).catch(() => null)
      const local = bmp ? await detectIn(bmp) : null
      if (local) { onFound(local); return }
      const r = await sendFile<{ found: Found | null }>('POST', `${V1}/scan`, blob)
      if (r.found) onFound(r.found)
      else setMiss('No barcode found in that photo. Take it closer, flat and sharp, or type the number in.')
    } catch (e) { setMiss((e as Error).message) } finally { setBusy(false) }
  }

  return (
    <Modal title="Scan the barcode" onClose={onClose} size="sm" className="scan-modal">
      <div className="scan-box">
        {state !== 'nocam' && <video ref={video} playsInline muted className="scan-video" />}
        {state === 'live' && <div className="scan-frame" aria-hidden="true" />}
        {state === 'starting' && <div className="scan-msg"><Spinner /> Opening the camera…</div>}
        {state === 'nocam' && <div className="scan-msg"><Icon name="camera" size={22} /><span>{why}</span></div>}
      </div>
      <p className="muted small">Hold the card flat, the code inside the frame. Shop cards, tickets and boarding passes all work.</p>
      {miss && <p className="bad small">{miss}</p>}
      <div className="form-actions">
        <label className="btn"><Icon name="camera" size={15} />{busy ? 'Reading…' : 'Use a photo'}<input type="file" accept="image/*" capture="environment" hidden onChange={e => { const f = e.target.files?.[0]; e.target.value = ''; if (f) photo(f) }} /></label>
        <button type="button" className="btn" onClick={onClose}>Type it in</button>
      </div>
    </Modal>
  )
}
