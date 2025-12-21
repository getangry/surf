# Surf Documentation

This is the documentation site for Surf, built with [Fumadocs](https://fumadocs.dev/) - a Mintlify-style open-source documentation framework.

## Getting Started

### Prerequisites

- Node.js 20 or higher
- pnpm (recommended) or npm

### Installation

```bash
cd _docs
pnpm install
```

### Development

```bash
pnpm dev
```

Open [http://localhost:3000](http://localhost:3000) to view the documentation.

### Build

```bash
pnpm build
```

### Production

```bash
pnpm start
```

## Project Structure

```
_docs/
├── content/
│   └── docs/           # MDX documentation files
│       ├── index.mdx
│       ├── installation.mdx
│       ├── quickstart.mdx
│       └── ...
├── src/
│   ├── app/            # Next.js App Router
│   │   ├── docs/       # Documentation pages
│   │   └── page.tsx    # Landing page
│   └── lib/
│       └── source.ts   # Fumadocs configuration
├── package.json
├── tailwind.config.js
└── next.config.mjs
```

## Writing Documentation

Documentation is written in MDX format in the `content/docs/` directory.

### Frontmatter

```mdx
---
title: Page Title
description: Brief description for SEO
---

# Content here
```

### Components

Fumadocs provides built-in components:

- `<Cards>` / `<Card>` - Feature cards with links
- `<Callout>` - Info, warning, and error callouts
- `<Steps>` - Step-by-step instructions
- `<Tabs>` / `<Tab>` - Tabbed content

### Adding New Pages

1. Create a new `.mdx` file in `content/docs/`
2. Add frontmatter with title and description
3. Add the page to `content/docs/meta.json`

## Customization

### Theme Colors

Edit `tailwind.config.js` to customize the color scheme.

### Navigation

Edit `src/app/layout.config.tsx` to customize navigation links.

## Deployment

The site can be deployed to:

- Vercel (recommended)
- Netlify
- Any static hosting

```bash
pnpm build
# Deploy the .next folder or use Vercel/Netlify integration
```
