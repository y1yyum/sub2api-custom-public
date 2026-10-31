/**
 * Nine-slice source for three 1px rules 1px apart, used as
 * `border-image: <frame> 5 / 5px`. `--pp-frame` in rhine-theme.css is this
 * frame in the edge colour; components tint it with their own rule colour.
 */
export function pixelFrame(color: string): string {
  return (
    "url(\"data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='11' height='11' shape-rendering='crispEdges'>" +
    `<path fill='${color.replace('#', '%23')}' fill-rule='evenodd' d='M0 0h11v11H0zM1 1v9h9V1zM2 2h7v7H2zM3 3v5h5V3zM4 4h3v3H4zM5 5v1h1V5z'/></svg>")`
  )
}
