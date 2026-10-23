import { useEffect, useRef, useState } from "react";
import { Mic, MicOff } from "lucide-react";
import { micConstraints, preferredInput, setPreferredInput } from "../lib/callAudio";
import { Alert, Button, Card, CardBody, CardHeader, Select } from "./ui";

// MicCheck lets the user pick the microphone and see its level without a
// call, so a silent input is found before the other side complains.
// bare drops the card around it, for use inside another section.
export function MicCheck({ bare }: { bare?: boolean }) {
  const [inputs, setInputs] = useState<MediaDeviceInfo[]>([]);
  const [choice, setChoice] = useState(preferredInput());
  const [testing, setTesting] = useState(false);
  const [level, setLevel] = useState(0);
  const [peak, setPeak] = useState(0);
  const [error, setError] = useState("");
  const stop = useRef<() => void>(() => {});

  const refresh = async () => {
    try {
      const all = await navigator.mediaDevices.enumerateDevices();
      setInputs(all.filter((d) => d.kind === "audioinput"));
    } catch {
      setInputs([]);
    }
  };
  useEffect(() => {
    refresh();
    return () => stop.current();
  }, []);

  const start = async () => {
    stop.current();
    setError("");
    setPeak(0);
    if (!window.isSecureContext) {
      setError(`浏览器只允许在 HTTPS 或 localhost 下使用麦克风，请用 https:// 开头的地址打开`);
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: micConstraints() });
      // Labels are only readable after permission was granted.
      refresh();
      const ctx = new AudioContext();
      const analyser = ctx.createAnalyser();
      analyser.fftSize = 1024;
      ctx.createMediaStreamSource(stream).connect(analyser);
      const buf = new Float32Array(analyser.fftSize);
      let raf = 0;
      const tick = () => {
        analyser.getFloatTimeDomainData(buf);
        let p = 0;
        for (const v of buf) p = Math.max(p, Math.abs(v));
        setLevel(p);
        setPeak((x) => Math.max(x, p));
        raf = requestAnimationFrame(tick);
      };
      tick();
      setTesting(true);
      stop.current = () => {
        cancelAnimationFrame(raf);
        stream.getTracks().forEach((t) => t.stop());
        ctx.close().catch(() => {});
        setTesting(false);
        setLevel(0);
      };
    } catch (e) {
      const name = (e as DOMException)?.name;
      setError(name === "NotAllowedError" ? "麦克风权限被拒绝，请在浏览器地址栏左侧的站点设置里允许麦克风" : name === "NotFoundError" || name === "OverconstrainedError" ? "找不到这个麦克风，请换一个" : "无法打开麦克风");
    }
  };

  const pick = (id: string) => {
    setChoice(id);
    setPreferredInput(id);
    if (testing) setTimeout(start, 0);
  };

  const silent = testing && peak < 0.002;
  const button = (
    <Button size="sm" icon={testing ? MicOff : Mic} variant={testing ? "secondary" : "primary"} onClick={testing ? () => stop.current() : start}>
      {testing ? "停止测试" : "测试麦克风"}
    </Button>
  );
  const body = (
    <>
      <Select value={choice} onChange={(e) => pick(e.target.value)}>
        <option value="">系统默认麦克风</option>
        {inputs
          .filter((d) => d.deviceId && d.deviceId !== "default")
          .map((d, i) => (
            <option key={d.deviceId} value={d.deviceId}>
              {d.label || `麦克风 ${i + 1}（授权后显示名称）`}
            </option>
          ))}
      </Select>
      <div className="h-3 overflow-hidden rounded-full bg-line-strong">
        <div className="h-full rounded-full bg-ok transition-[width] duration-75" style={{ width: `${Math.min(100, Math.round(level * 140))}%` }} />
      </div>
      {testing && !silent && <p className="text-xs text-ok">对着麦克风说话，绿色条会跟着跳动。</p>}
      {silent && (
        <Alert tone="warn" title="还没有检测到声音">
          请对着麦克风说几句话。如果一直没有反应：macOS 打开“系统设置 → 隐私与安全性 → 麦克风”，确认浏览器已开启；再到“系统设置 → 声音 → 输入”确认选中了正确的麦克风、输入音量不为 0；或者在上面换一个麦克风。
        </Alert>
      )}
      {error && <p className="text-xs text-bad">{error}</p>}
    </>
  );
  if (bare) {
    return (
      <div className="space-y-3">
        {body}
        {button}
      </div>
    );
  }
  return (
    <Card>
      <CardHeader icon={Mic} title="麦克风" subtitle="通话前先确认麦克风有声音" action={button} />
      <CardBody className="space-y-3 pt-3">{body}</CardBody>
    </Card>
  );
}
