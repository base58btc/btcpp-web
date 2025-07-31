SUBDIR="${1:-*}"
TYPE="${2:-*}"
for file in $SUBDIR/$TYPE/*.pdf; do
	name=${file%%.*}
	pdftoppm -q -png $file $name
done
