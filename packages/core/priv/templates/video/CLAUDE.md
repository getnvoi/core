# A video

A Remotion composition (src/Main.tsx) rendered to an MP4 by nvoi on this machine after each save.

- What the video says and how long it is lives in props.json; change it there first. src/Main.tsx draws one frame
  from the props and the frame number (`useCurrentFrame`); animate with `interpolate` and `spring` from remotion.
- Keep src/props.ts in step with props.json. 1920x1080 at 30 frames a second.
- Assets: put images in public/ and use `staticFile("name.png")`; fonts and scripts inline or from public/.
- `npm run build` must pass: it type-checks and builds dist/index.html, the preview members play before the
  render. save_artifact runs it and refuses the version when it fails, then renders the MP4 in the background.
- Do not run `npm run render` yourself unless you need to check a frame: nvoi renders after the save.
