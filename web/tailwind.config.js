/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{ts,tsx}'],
  corePlugins: {
    preflight: false,
  },
  theme: {
    extend: {
      // Semantic design tokens — values mirror app.tapnow.ai's CSS variables
      // so our Tailwind utilities and TapNow's renderer share one palette.
      // Use these by name (bg-card, text-muted-foreground, …) instead of
      // raw zinc/blue scales going forward.
      colors: {
        background: '#000000',
        foreground: '#f5f5f5',
        card: {
          DEFAULT: '#1f1f1f',
          foreground: '#fafafa',
        },
        popover: {
          DEFAULT: '#262626',
          foreground: '#cccccc',
        },
        muted: {
          DEFAULT: '#2b2b2b',
          foreground: '#7a7a7a',
        },
        accent: {
          DEFAULT: '#404040',
          foreground: '#fafafa',
        },
        sidebar: {
          DEFAULT: '#1c1c1c',
          foreground: '#cfcece',
          accent: '#262626',
          'accent-foreground': '#fafafa',
          border: 'rgba(255,255,255,0.1)',
        },
        primary: {
          DEFAULT: '#1fa2dc',
          foreground: '#fafafa',
        },
        destructive: {
          DEFAULT: '#934c4c',
          foreground: '#ff6163',
        },
        border: 'rgba(255,255,255,0.10)',
        input: 'rgba(255,255,255,0.15)',
        ring: 'rgba(115,115,115,0.10)',
        // Legacy alias — values already aligned to TapNow tokens above.
        // Existing components using `canvas-*` will pick up the new look
        // without code changes.
        canvas: {
          bg: '#000000',
          surface: '#1f1f1f',
          border: 'rgba(255,255,255,0.10)',
          muted: '#7a7a7a',
          text: '#f5f5f5',
          dim: '#cfcece',
          accent: '#1fa2dc',
        },
      },
      borderRadius: {
        xl: '0.75rem',
        '2xl': '1rem',
        '3xl': '1.5rem',
        '4xl': '2rem',
      },
      fontFamily: {
        sans: ['Inter', 'system-ui', '-apple-system', 'BlinkMacSystemFont', 'Segoe UI', 'sans-serif'],
        mono: ['JetBrains Mono', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
    },
  },
  plugins: [],
};
