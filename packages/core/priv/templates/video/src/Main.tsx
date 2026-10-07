// The video itself: change it freely. Everything it shows comes from props.json, so a new text or length is a
// change to props.json alone.
import { AbsoluteFill, interpolate, useCurrentFrame, useVideoConfig } from "remotion";
import type { Props } from "./props";

export function Main({ title, subtitle, background, color }: Props) {
  const frame = useCurrentFrame();
  const { fps } = useVideoConfig();
  const opacity = interpolate(frame, [0, fps], [0, 1], { extrapolateRight: "clamp" });
  const rise = interpolate(frame, [0, fps], [24, 0], { extrapolateRight: "clamp" });
  return (
    <AbsoluteFill style={{ background, color, alignItems: "center", justifyContent: "center", fontFamily: "Inter, system-ui, sans-serif" }}>
      <div style={{ opacity, transform: `translateY(${rise}px)`, textAlign: "center" }}>
        <div style={{ fontSize: 120, fontWeight: 600, letterSpacing: -2 }}>{title}</div>
        <div style={{ fontSize: 48, opacity: 0.6, marginTop: 24 }}>{subtitle}</div>
      </div>
    </AbsoluteFill>
  );
}
