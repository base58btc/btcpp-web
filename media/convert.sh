SUBDIR="${1:-*}"
for file in $SUBDIR/*.pdf; do
	name=${file%%.*}
	pdftoppm -q -png $file $name
done
