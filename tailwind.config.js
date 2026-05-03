/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ["./templates/*.tmpl", "./templates/*/*.tmpl", "./internal/helpers/*.go"],
  theme: {
    extend: { 
		colors: {
			bitcoin: {
				DEFAULT: '#FFA800',		   
			},
			buenos: {
				DEFAULT: '#F0452B',
			},
			pills: {
				DEFAULT: '#034EA4',
			},
			txgreen: {
				DEFAULT: '#388D60',
			},
		},
		fontFamily: {
			bitcoin: ['Ubuntu-BoldItalic', 'sans-serif'],
			arial: ['Arial'],
		},
    },
  },
  plugins: [
	  require('@tailwindcss/forms'),
	  require('@tailwindcss/typography'),
  ],
}
