/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        // 主色调 - 与首页一致的墨绿 / 鼠尾草绿
        primary: {
          50: '#f0f4e8',
          100: '#dce7c7',
          200: '#c3d5ac',
          300: '#a6c193',
          400: '#82ab83',
          500: '#295747',
          600: '#173f35',
          700: '#12372e',
          800: '#102f28',
          900: '#0d2923',
          950: '#081d18'
        },
        // 中性色统一偏暖；保留状态色与模型品牌色。
        gray: {
          50: '#f5f4ed',
          100: '#eeeee5',
          200: '#d9dfd3',
          300: '#bdc8b9',
          400: '#879584',
          500: '#657461',
          600: '#52644f',
          700: '#3e5544',
          800: '#294b3c',
          900: '#173f35',
          950: '#102c25'
        },
        // 辅助中性色 - 绿色调灰阶
        accent: {
          50: '#f5f4ed',
          100: '#eeeee5',
          200: '#d9dfd3',
          300: '#bdc8b9',
          400: '#879584',
          500: '#657461',
          600: '#52644f',
          700: '#3e5544',
          800: '#294b3c',
          900: '#173f35',
          950: '#102c25'
        },
        // 深色模式背景
        dark: {
          50: '#f5f4ed',
          100: '#e8ede3',
          200: '#d5dfd0',
          300: '#b6c6b7',
          400: '#90a596',
          500: '#6e8979',
          600: '#4c695b',
          700: '#314d40',
          800: '#20392f',
          900: '#14291f',
          950: '#0c1c16'
        }
      },
      fontFamily: {
        sans: [
          'system-ui',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'sans-serif'
        ],
        mono: ['ui-monospace', 'SFMono-Regular', 'Menlo', 'Monaco', 'Consolas', 'monospace']
      },
      boxShadow: {
        glass: '0 8px 32px rgba(0, 0, 0, 0.08)',
        'glass-sm': '0 4px 16px rgba(0, 0, 0, 0.06)',
        glow: '0 0 20px rgba(41, 87, 71, 0.25)',
        'glow-lg': '0 0 40px rgba(41, 87, 71, 0.35)',
        card: '0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06)',
        'card-hover': '0 10px 40px rgba(0, 0, 0, 0.08)',
        'inner-glow': 'inset 0 1px 0 rgba(255, 255, 255, 0.1)'
      },
      backgroundImage: {
        'gradient-radial': 'radial-gradient(var(--tw-gradient-stops))',
        'gradient-primary': 'linear-gradient(135deg, #295747 0%, #173f35 100%)',
        'gradient-dark': 'linear-gradient(135deg, #20392f 0%, #14291f 100%)',
        'gradient-glass':
          'linear-gradient(135deg, rgba(255,255,255,0.1) 0%, rgba(255,255,255,0.05) 100%)',
        'mesh-gradient':
          'radial-gradient(at 40% 20%, rgba(130, 171, 131, 0.12) 0px, transparent 50%), radial-gradient(at 80% 0%, rgba(231, 120, 70, 0.08) 0px, transparent 50%), radial-gradient(at 0% 50%, rgba(130, 171, 131, 0.08) 0px, transparent 50%)'
      },
      animation: {
        'fade-in': 'fadeIn 0.3s ease-out',
        'slide-up': 'slideUp 0.3s ease-out',
        'slide-down': 'slideDown 0.3s ease-out',
        'slide-in-right': 'slideInRight 0.3s ease-out',
        'scale-in': 'scaleIn 0.2s ease-out',
        'pulse-slow': 'pulse 3s cubic-bezier(0.4, 0, 0.6, 1) infinite',
        shimmer: 'shimmer 2s linear infinite',
        glow: 'glow 2s ease-in-out infinite alternate'
      },
      keyframes: {
        fadeIn: {
          '0%': { opacity: '0' },
          '100%': { opacity: '1' }
        },
        slideUp: {
          '0%': { opacity: '0', transform: 'translateY(10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideDown: {
          '0%': { opacity: '0', transform: 'translateY(-10px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' }
        },
        slideInRight: {
          '0%': { opacity: '0', transform: 'translateX(20px)' },
          '100%': { opacity: '1', transform: 'translateX(0)' }
        },
        scaleIn: {
          '0%': { opacity: '0', transform: 'scale(0.95)' },
          '100%': { opacity: '1', transform: 'scale(1)' }
        },
        shimmer: {
          '0%': { backgroundPosition: '-200% 0' },
          '100%': { backgroundPosition: '200% 0' }
        },
        glow: {
          '0%': { boxShadow: '0 0 20px rgba(20, 184, 166, 0.25)' },
          '100%': { boxShadow: '0 0 30px rgba(20, 184, 166, 0.4)' }
        }
      },
      backdropBlur: {
        xs: '2px'
      },
      borderRadius: {
        '4xl': '2rem'
      }
    }
  },
  plugins: []
}
