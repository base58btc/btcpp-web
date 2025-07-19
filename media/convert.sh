for file in */*.pdf; do
	name=${file%%.*}
	pdftoppm -q -png $file $name
done
