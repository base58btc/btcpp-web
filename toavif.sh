DIR="${1:-*}"
for file in static/img/$DIR/*.png; do
	name=${file%%.*}
        ffmpeg -n -i "$file" -c:v libaom-av1 -still-picture 1 "$name".avif
done
