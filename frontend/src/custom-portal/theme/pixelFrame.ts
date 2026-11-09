/**
 * Nine-slice source for three 1px rules 1px apart, used as
 * `border-image: <frame> 5 / 5px`. `--pp-frame` (theme/palettes.ts) is this
 * frame in the edge colour; components tint it with their own rule colour.
 */
export function pixelFrame(color: string): string {
  return (
    "url(\"data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='11' height='11' shape-rendering='crispEdges'>" +
    `<path fill='${color.replace('#', '%23')}' fill-rule='evenodd' d='M0 0h11v11H0zM1 1v9h9V1zM2 2h7v7H2zM3 3v5h5V3zM4 4h3v3H4zM5 5v1h1V5z'/></svg>")`
  )
}

/** 8×8 square-spiral tile for the band under page titles, in `color`. */
export function pixelHuiwen(color: string): string {
  return (
    "url(\"data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' width='8' height='8' shape-rendering='crispEdges'>" +
    `<path fill='${color.replace('#', '%23')}' d='M0 0h7v1h-7zM0 1h1v1h-1zM6 1h1v1h-1zM0 2h1v1h-1zM2 2h3v1h-3zM6 2h1v1h-1zM0 3h1v1h-1zM2 3h1v1h-1zM4 3h1v1h-1zM6 3h1v1h-1zM0 4h1v1h-1zM2 4h1v1h-1zM6 4h1v1h-1zM0 5h1v1h-1zM2 5h5v1h-5zM0 6h1v1h-1zM0 7h8v1h-8z'/></svg>")`
  )
}
