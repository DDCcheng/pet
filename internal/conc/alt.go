package conc

func Alternate() string{
	num_chan:=make(chan string)
	alpha_chan:=make(chan string)

	go func(){
		for _,n :=range []string{"1","2","3","4"}{
			num_chan<-n
		}
	}()

	go func(){
		for _,n :=range []string{"a","b","c","d"}{
			alpha_chan<-n
		}
	}()
	out :=""
	for i :=0;i<4;i++{
		out+=<-num_chan
		out+=<-alpha_chan
	}
	return out
}